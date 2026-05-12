package recording

import (
	"encoding/json"
	"errors"
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
