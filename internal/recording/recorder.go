package recording

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/storage"
)

// StorageStats contains aggregate storage information.
type StorageStats struct {
	TotalBytes      int64            `json:"total_bytes"`
	SegmentCount    int              `json:"segment_count"`
	CameraStats     map[string]int64 `json:"camera_stats"`
	DiskAvailable   uint64           `json:"disk_available_bytes"`
	DiskLow         bool             `json:"disk_low"`
	RecordingPaused bool             `json:"recording_paused"`
}

// Recorder manages saving video clips for detected events.
type Recorder struct {
	config       config.RecordingConfig
	eventConfig  config.EventConfig
	db           *storage.DB
	hub          *rtsp.Hub
	segments     *SegmentRecorder
	cameraURLs   map[string]string // camera name → record RTSP URL
	startTime    time.Time
	snapshotPath string

	// Cached storage stats refreshed in background
	statsMu     sync.RWMutex
	cachedStats StorageStats
}

func New(cfg config.RecordingConfig, eventCfg config.EventConfig, db *storage.DB, hub *rtsp.Hub, snapshotPath string) *Recorder {
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		slog.Error("failed to create recording directory", "path", cfg.Path, "error", err)
	}

	// Clean up stale export temp files from previous runs
	exportDir := filepath.Join(cfg.Path, ".exports")
	os.RemoveAll(exportDir)

	return &Recorder{
		config:       cfg,
		eventConfig:  eventCfg,
		db:           db,
		hub:          hub,
		segments:     NewSegmentRecorder(cfg, db, hub),
		cameraURLs:   make(map[string]string),
		startTime:    time.Now(),
		snapshotPath: snapshotPath,
	}
}

// RegisterCamera registers a camera's recording URL for direct-from-stream recording.
func (r *Recorder) RegisterCamera(name, rtspURL string) {
	r.cameraURLs[name] = rtspURL
}

// CameraURL returns the recording URL for a camera, or empty string if not registered.
func (r *Recorder) CameraURL(name string) string {
	return r.cameraURLs[name]
}

// StartTemporaryRecording begins segment recording for a single camera.
// Used when continuous recording is off but an event requires video.
// Cancel the context to stop recording.
func (r *Recorder) StartTemporaryRecording(ctx context.Context, cameraName, rtspURL string) {
	r.segments.StartRecording(ctx, cameraName, rtspURL)
}

// StartContinuousRecording begins segment recording for all registered cameras.
func (r *Recorder) StartContinuousRecording(ctx context.Context) {
	if !r.config.Continuous {
		slog.Info("continuous recording disabled")
		return
	}

	first := true
	for name, url := range r.cameraURLs {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
		r.segments.StartRecording(ctx, name, url)
		first = false
	}

	// Reconcile filesystem with database in the background to avoid blocking startup.
	go func() {
		for name := range r.cameraURLs {
			segDir := filepath.Join(r.config.Path, name, "segments")
			r.segments.ScanExistingSegments(name, segDir)
		}
	}()

	slog.Info("continuous recording started", "cameras", len(r.cameraURLs))
}

// SaveClip records a clip around the event timestamp.
func (r *Recorder) SaveClip(_ context.Context, event camera.Event) error {
	clipPath, err := r.ExtractClip(context.Background(), event)
	if err != nil {
		return fmt.Errorf("extract clip: %w", err)
	}

	if err := r.db.UpdateEventClipPath(event.ID, clipPath); err != nil {
		slog.Error("failed to update event clip path", "error", err)
	}

	slog.Info("clip saved",
		"camera", event.CameraName,
		"label", event.Label,
		"path", clipPath,
	)

	return nil
}

// Close waits for all recording goroutines to finalize their segments,
// with a timeout to prevent hanging on shutdown.
func (r *Recorder) Close() {
	done := make(chan struct{})
	go func() {
		r.segments.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("all recording segments finalized")
	case <-time.After(10 * time.Second):
		slog.Warn("timed out waiting for recording segments to finalize")
	}
}

// StartStatsRefresh begins a background loop that periodically refreshes
// cached storage stats. This prevents API handlers from blocking on DB queries.
func (r *Recorder) StartStatsRefresh(ctx context.Context) {
	go func() {
		// Initial refresh in the goroutine to avoid blocking startup
		// when segment scanning holds the DB connection.
		r.RefreshStats()

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.RefreshStats()
			}
		}
	}()
}

// RefreshStats queries the database and updates the cached stats.
func (r *Recorder) RefreshStats() {
	stats := StorageStats{
		CameraStats: make(map[string]int64),
	}

	totalBytes, err := r.db.TotalSegmentBytes()
	if err != nil {
		slog.Error("failed to query total segment bytes", "error", err)
	} else {
		stats.TotalBytes = totalBytes
	}

	count, err := r.db.CountSegments()
	if err != nil {
		slog.Error("failed to query segment count", "error", err)
	} else {
		stats.SegmentCount = count
	}

	byCamera, err := r.db.SegmentBytesByCamera()
	if err != nil {
		slog.Error("failed to query segment bytes by camera", "error", err)
	} else {
		stats.CameraStats = byCamera
	}

	stats.DiskAvailable = r.segments.DiskAvailable()
	stats.DiskLow = stats.DiskAvailable < media.MinDiskSpace
	stats.RecordingPaused = r.segments.AnyPaused()

	r.statsMu.Lock()
	r.cachedStats = stats
	r.statsMu.Unlock()
}

// StorageStats returns cached aggregate storage information.
// Updated in background every 10s by StartStatsRefresh.
func (r *Recorder) StorageStats() StorageStats {
	r.statsMu.RLock()
	defer r.statsMu.RUnlock()
	return r.cachedStats
}

// DiskAvailable returns the bytes available on the recording filesystem.
func (r *Recorder) DiskAvailable() uint64 {
	return r.segments.DiskAvailable()
}

// HasSegments returns true if there are any segments covering the given time for a camera.
func (r *Recorder) HasSegments(cameraName string, t time.Time) bool {
	// Check a small window around the timestamp
	from := t.Add(-1 * time.Second)
	to := t.Add(1 * time.Second)
	segments := r.segments.FindSegments(cameraName, from, to)
	return len(segments) > 0
}

// ExportResult holds a prepared export ready to be served via http.ServeContent.
// The caller must call Close when done.
type ExportResult struct {
	File    *os.File
	tmpPath string
}

// Close cleans up the file handle and temporary export file.
func (er *ExportResult) Close() {
	er.File.Close()
	os.Remove(er.tmpPath)
}

// PrepareExport builds an MP4 covering [from, to) for a camera and returns
// a result that can be streamed. This separates validation/preparation
// (which can fail with a proper error) from streaming (which happens after
// HTTP headers are sent).
func (r *Recorder) PrepareExport(cameraName string, from, to time.Time) (*ExportResult, error) {
	segments := r.segments.FindSegments(cameraName, from, to)
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments found for camera %q in range %s–%s", cameraName, from.Format(time.RFC3339), to.Format(time.RFC3339))
	}

	// Filter out segments whose files no longer exist
	valid := segments[:0]
	for _, seg := range segments {
		if _, err := os.Stat(seg.Path); err == nil {
			valid = append(valid, seg)
		}
	}
	segments = valid
	if len(segments) == 0 {
		return nil, fmt.Errorf("segments deleted before export for camera %q", cameraName)
	}

	startOffset := from.Sub(segments[0].StartTime)
	if startOffset < 0 {
		startOffset = 0
	}
	duration := to.Sub(from)

	exportDir := filepath.Join(r.config.Path, ".exports")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return nil, fmt.Errorf("create export dir: %w", err)
	}
	tmpFile, err := os.CreateTemp(exportDir, "export-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	inputs := make([]string, len(segments))
	for i, seg := range segments {
		inputs[i] = seg.Path
	}

	// Run trim/concat with a timeout to prevent hanging on corrupt segments.
	// Generous limit: 2 minutes per input segment.
	timeout := time.Duration(len(inputs)) * 2 * time.Minute
	errCh := make(chan error, 1)
	go func() {
		if len(inputs) == 1 {
			errCh <- media.TrimMP4(inputs[0], tmpPath, startOffset, duration)
		} else {
			errCh <- media.ConcatMP4(inputs, tmpPath, startOffset, duration)
		}
	}()

	select {
	case err := <-errCh:
		if err != nil {
			os.Remove(tmpPath)
			return nil, fmt.Errorf("process segments: %w", err)
		}
	case <-time.After(timeout):
		os.Remove(tmpPath)
		return nil, fmt.Errorf("export timed out after %s (possible corrupt segment)", timeout)
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("stat export: %w", err)
	}
	if info.Size() == 0 {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("no video data in the requested range for camera %q", cameraName)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("open export result: %w", err)
	}

	return &ExportResult{
		File:    f,
		tmpPath: tmpPath,
	}, nil
}

// ListSegmentsForDate returns segments for a camera on a specific date.
func (r *Recorder) ListSegmentsForDate(cameraName string, date time.Time) []storage.SegmentRecord {
	segments, err := r.db.GetSegmentsForDate(cameraName, date)
	if err != nil {
		slog.Error("failed to query segments for date",
			"camera", cameraName,
			"date", date,
			"error", err,
		)
		return nil
	}
	return segments
}
