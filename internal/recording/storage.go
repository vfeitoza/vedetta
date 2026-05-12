package recording

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// ErrStorageBusy is returned when segmentOpMu cannot be acquired
// via TryLock. The HTTP layer maps this to 409 + Retry-After: 5.
var ErrStorageBusy = errors.New("storage operation in progress")

// ErrOpenSegmentProtected is returned when a delete request explicitly
// targets a currently-open segment. The HTTP layer maps this to 422
// with the protected paths in the body.
type ErrOpenSegmentProtected struct {
	Paths []string
}

func (e *ErrOpenSegmentProtected) Error() string {
	return "request targets currently-open segment(s)"
}

// FilesystemStats describes one storage root.
type FilesystemStats struct {
	UsedBytes      int64  `json:"used_bytes"`
	SegmentBytes   int64  `json:"segment_bytes,omitempty"`
	ClipBytes      int64  `json:"clip_bytes,omitempty"`
	DiskAvailable  int64  `json:"disk_available"`
	Root           string `json:"root"`
	SameFilesystem bool   `json:"same_filesystem_as_recording,omitempty"`
}

// CameraBreakdown is one row of the per-camera table.
type CameraBreakdown struct {
	Name                string     `json:"name"`
	SegmentBytes        int64      `json:"segment_bytes"`
	ClipBytes           int64      `json:"clip_bytes"`
	OldestSegment       *time.Time `json:"oldest_segment,omitempty"`
	Last7dBytes         int64      `json:"last_7d_bytes"`
	PerDay              []DayBytes `json:"per_day"`
	EffectiveRetainDays int        `json:"effective_retain_days"`
}

// DayBytes is one bar of the 30-day chart.
type DayBytes struct {
	Date  string `json:"date"` // YYYY-MM-DD, UTC
	Bytes int64  `json:"bytes"`
}

// StorageBreakdown is the response shape of GET /api/storage.
type StorageBreakdown struct {
	Recording       FilesystemStats   `json:"recording"`
	Snapshots       FilesystemStats   `json:"snapshots"`
	RecordingPaused bool              `json:"recording_paused"`
	Projection      any               `json:"projection"`
	Cameras         []CameraBreakdown `json:"cameras"`
}

// DeleteTarget enumerates the four valid delete shapes.
type DeleteTarget string

const (
	DeleteSegments  DeleteTarget = "segments"
	DeleteClips     DeleteTarget = "clips"
	DeleteAll       DeleteTarget = "all"
	DeleteFreeBytes DeleteTarget = "free_bytes"
)

// DeleteRequest is the input to Recorder.DeleteStorage.
type DeleteRequest struct {
	Target          DeleteTarget `json:"target"`
	Camera          string       `json:"camera,omitempty"`
	OlderThanDays   int          `json:"older_than_days,omitempty"`
	From            string       `json:"from,omitempty"` // YYYY-MM-DD
	To              string       `json:"to,omitempty"`   // YYYY-MM-DD
	FreeBytesTarget int64        `json:"free_bytes_target,omitempty"`
	DryRun          bool         `json:"-"` // populated from query string
}

// DeleteResult is the response shape of POST /api/storage/delete.
type DeleteResult struct {
	Segments  int      `json:"segments"`
	Clips     int      `json:"clips"`
	Snapshots int      `json:"snapshots"`
	Bytes     int64    `json:"bytes"`
	Cameras   []string `json:"cameras"`
}

// MarshalScope serializes a DeleteRequest into the JSON shape stored
// in storage_audit.scope_json.
func (req DeleteRequest) MarshalScope() string {
	b, _ := json.Marshal(req)
	return string(b)
}

// breakdownCache is the in-process 30s cache of StorageBreakdown.
type breakdownCache struct {
	mu      sync.RWMutex
	value   *StorageBreakdown
	storeAt time.Time
}

const breakdownTTL = 30 * time.Second

// StorageBreakdown returns the cached per-camera storage figures used
// by GET /api/storage. The cache TTL is 30s.
func (r *Recorder) StorageBreakdown() (*StorageBreakdown, error) {
	if v := r.cachedBreakdown(); v != nil {
		return v, nil
	}

	openByCamera := make(map[string]string)
	if r.segments != nil {
		for _, s := range r.segments.CurrentSegmentPaths() {
			openByCamera[s.Camera] = s.Path
		}
	}

	out := &StorageBreakdown{
		Recording: FilesystemStats{Root: r.config.Path},
		Snapshots: FilesystemStats{Root: r.snapshotPath},
	}
	out.Recording.DiskAvailable = statfsAvailable(r.config.Path)
	out.Snapshots.DiskAvailable = statfsAvailable(r.snapshotPath)
	out.Snapshots.SameFilesystem = sameFilesystem(r.config.Path, r.snapshotPath)

	for _, cam := range r.cameraNames() {
		cb := r.cameraBreakdown(cam, openByCamera[cam])
		out.Cameras = append(out.Cameras, cb)
		out.Recording.SegmentBytes += cb.SegmentBytes
		out.Recording.ClipBytes += cb.ClipBytes
	}
	out.Recording.UsedBytes = out.Recording.SegmentBytes + out.Recording.ClipBytes
	out.Snapshots.UsedBytes = r.totalSnapshotBytes()
	out.RecordingPaused = r.recordingPaused()
	out.Projection = r.projection()

	r.storeBreakdown(out)
	return out, nil
}

func (r *Recorder) cachedBreakdown() *StorageBreakdown {
	r.breakdownCache.mu.RLock()
	defer r.breakdownCache.mu.RUnlock()
	if r.breakdownCache.value != nil && time.Since(r.breakdownCache.storeAt) < breakdownTTL {
		return r.breakdownCache.value
	}
	return nil
}

func (r *Recorder) storeBreakdown(v *StorageBreakdown) {
	r.breakdownCache.mu.Lock()
	defer r.breakdownCache.mu.Unlock()
	r.breakdownCache.value = v
	r.breakdownCache.storeAt = time.Now()
}

func (r *Recorder) cameraNames() []string {
	names := make([]string, 0, len(r.cameraURLs))
	for name := range r.cameraURLs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Recorder) effectiveRetainDays(cam string) int {
	if v, ok := r.cameraRetention[cam]; ok {
		return v
	}
	return r.config.RetainDays
}

func (r *Recorder) recordingPaused() bool {
	if r.segments == nil {
		return false
	}
	return r.segments.AnyPaused()
}

func (r *Recorder) projection() any {
	if r.segments == nil {
		return nil
	}
	stats := r.StorageStats()
	return r.computeProjection(&stats)
}

// cameraBreakdown computes one row, including a stat of the currently
// open segment (if any) so the live segment isn't reported as 0 bytes.
func (r *Recorder) cameraBreakdown(camera, openPath string) CameraBreakdown {
	cb := CameraBreakdown{
		Name:                camera,
		EffectiveRetainDays: r.effectiveRetainDays(camera),
	}
	if r.db != nil {
		farPast := time.Unix(0, 0)
		farFuture := time.Now().AddDate(100, 0, 0)
		if segs, err := r.db.SegmentsByCameraInRange(camera, farPast, farFuture); err == nil {
			for _, s := range segs {
				cb.SegmentBytes += s.SizeBytes
				if cb.OldestSegment == nil || s.StartTime.Before(*cb.OldestSegment) {
					ts := s.StartTime
					cb.OldestSegment = &ts
				}
			}
		}
	}
	if openPath != "" {
		if fi, err := os.Stat(openPath); err == nil {
			cb.SegmentBytes += fi.Size()
		}
	}
	clipsRoot := filepath.Join(r.config.Path, camera, "clips")
	_ = filepath.WalkDir(clipsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			cb.ClipBytes += fi.Size()
		}
		return nil
	})
	cb.PerDay = r.perDayBytes(camera, 30)
	cutoff := len(cb.PerDay) - 7
	if cutoff < 0 {
		cutoff = 0
	}
	for _, d := range cb.PerDay[cutoff:] {
		cb.Last7dBytes += d.Bytes
	}
	return cb
}

func (r *Recorder) perDayBytes(camera string, days int) []DayBytes {
	if r.db == nil {
		return nil
	}
	rows, err := r.db.PerDayCameraSegmentBytes(camera, days)
	if err != nil {
		return nil
	}
	out := make([]DayBytes, 0, len(rows))
	for _, row := range rows {
		out = append(out, DayBytes(row))
	}
	return out
}

func (r *Recorder) totalSnapshotBytes() int64 {
	if r.db == nil {
		return 0
	}
	paths, err := r.db.AllSnapshotPaths()
	if err != nil {
		return 0
	}
	var sum int64
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			sum += fi.Size()
		}
	}
	return sum
}

func statfsAvailable(root string) int64 {
	var s syscall.Statfs_t
	if err := syscall.Statfs(root, &s); err != nil {
		return 0
	}
	return int64(s.Bavail) * int64(s.Bsize)
}

func sameFilesystem(a, b string) bool {
	var sa, sb syscall.Statfs_t
	if err := syscall.Statfs(a, &sa); err != nil {
		return false
	}
	if err := syscall.Statfs(b, &sb); err != nil {
		return false
	}
	return sa.Fsid == sb.Fsid
}
