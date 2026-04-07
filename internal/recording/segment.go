package recording

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/storage"
)

// Segment represents a recorded video file covering a time range.
type Segment struct {
	Path      string
	Camera    string
	StartTime time.Time
	EndTime   time.Time
}

// SegmentRecorder continuously records RTSP streams into fixed-length segments
// using the native Go media pipeline (no ffmpeg).
type SegmentRecorder struct {
	config    config.RecordingConfig
	baseDir   string
	db        *storage.DB
	hub       *rtsp.Hub
	disk      *media.DiskSpace
	wg        sync.WaitGroup
	mu        sync.Mutex
	consumers []*media.RecordingConsumer
}

func NewSegmentRecorder(cfg config.RecordingConfig, db *storage.DB, hub *rtsp.Hub) *SegmentRecorder {
	baseDir := cfg.Path
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	return &SegmentRecorder{
		config:  cfg,
		baseDir: baseDir,
		db:      db,
		hub:     hub,
		disk:    media.NewDiskSpace(baseDir),
	}
}

// StartRecording begins continuous segment recording for a camera
// by creating a RecordingConsumer that receives RTP packets from the Hub.
func (sr *SegmentRecorder) StartRecording(ctx context.Context, cameraName, rtspURL string) {
	segDir := filepath.Join(sr.baseDir, cameraName, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		slog.Error("failed to create segment directory", "camera", cameraName, "error", err)
		return
	}

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		sr.recordLoop(ctx, cameraName, rtspURL, segDir)
	}()
}

// Wait blocks until all recording goroutines have finished and finalized their segments.
func (sr *SegmentRecorder) Wait() {
	sr.wg.Wait()
}

// DiskAvailable returns the bytes available on the recording filesystem.
func (sr *SegmentRecorder) DiskAvailable() uint64 {
	if sr.disk == nil {
		return 0
	}
	return sr.disk.Available()
}

// AnyPaused returns true if any recording consumer is paused due to low disk space.
func (sr *SegmentRecorder) AnyPaused() bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	for _, c := range sr.consumers {
		if c.Paused() {
			return true
		}
	}
	return false
}

func (sr *SegmentRecorder) recordLoop(ctx context.Context, cameraName, rtspURL, segDir string) {
	source := sr.hub.GetOrCreate(rtspURL)

	// Wait for connection and track info
	var videoTrack *rtsp.TrackInfo
	for {
		videoTrack = source.VideoTrack()
		if videoTrack != nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	audioTrack := source.AudioTrack()

	slog.Info("starting native recording",
		"camera", cameraName,
		"video", videoTrack.Codec,
		"audio_available", audioTrack != nil,
	)

	segmentLen := sr.config.SegmentLength
	if segmentLen == 0 {
		segmentLen = 10 * time.Minute
	}

	db := sr.db
	consumer := media.NewRecordingConsumer(segDir, cameraName, segmentLen, videoTrack, audioTrack, sr.disk, func(info media.SegmentInfo) {
		rec := storage.SegmentRecord{
			Camera:    info.Camera,
			Path:      info.Path,
			StartTime: info.StartTime,
			EndTime:   info.EndTime,
			SizeBytes: info.SizeBytes,
		}
		if err := db.SaveSegment(rec); err != nil {
			slog.Error("failed to save segment to database", "path", info.Path, "error", err)
		}
	})

	sr.mu.Lock()
	sr.consumers = append(sr.consumers, consumer)
	sr.mu.Unlock()

	source.AddConsumer(consumer)
	defer source.RemoveConsumer(consumer)
	defer consumer.Close()
	defer func() {
		sr.mu.Lock()
		for i, c := range sr.consumers {
			if c == consumer {
				sr.consumers = append(sr.consumers[:i], sr.consumers[i+1:]...)
				break
			}
		}
		sr.mu.Unlock()
	}()

	// Block until context is cancelled — the Hub handles reconnection
	<-ctx.Done()
}

// FindSegments returns segments for a camera that overlap the given time range.
func (sr *SegmentRecorder) FindSegments(cameraName string, from, to time.Time) []Segment {
	records, err := sr.db.QuerySegments(cameraName, from, to)
	if err != nil {
		slog.Error("failed to query segments from database", "camera", cameraName, "error", err)
		return nil
	}

	segments := make([]Segment, len(records))
	for i, r := range records {
		segments[i] = Segment{
			Path:      r.Path,
			Camera:    r.Camera,
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
	}
	return segments
}

// RemoveSegment deletes a segment file and removes it from the database.
func (sr *SegmentRecorder) RemoveSegment(cameraName string, segPath string) error {
	if err := os.Remove(segPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := sr.db.DeleteSegment(segPath); err != nil {
		return fmt.Errorf("delete segment from database: %w", err)
	}
	return nil
}

// AllSegments returns all segments for a given camera.
func (sr *SegmentRecorder) AllSegments(cameraName string) []Segment {
	records, err := sr.db.GetAllSegments(cameraName)
	if err != nil {
		slog.Error("failed to get all segments from database", "camera", cameraName, "error", err)
		return nil
	}

	segments := make([]Segment, len(records))
	for i, r := range records {
		segments[i] = Segment{
			Path:      r.Path,
			Camera:    r.Camera,
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
	}
	return segments
}

// ScanExistingSegments reconciles the filesystem with the database for a camera.
func (sr *SegmentRecorder) ScanExistingSegments(cameraName, segDir string) {
	slog.Info("scanning existing segments", "camera", cameraName, "dir", segDir)

	// Clean up stale temp files
	tempEntries, _ := os.ReadDir(segDir)
	for _, entry := range tempEntries {
		name := entry.Name()
		if strings.HasSuffix(name, ".remux.mp4") || strings.HasSuffix(name, ".mp4.tmp") {
			tmpPath := filepath.Join(segDir, name)
			slog.Warn("removing stale temp file", "path", tmpPath)
			_ = os.Remove(tmpPath)
		}
	}

	// Build set of files on disk
	diskFiles := make(map[string]os.FileInfo)
	entries, err := os.ReadDir(segDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read segment directory", "dir", segDir, "error", err)
		}
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		fullPath := filepath.Join(segDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		diskFiles[fullPath] = info
	}

	// Check DB records against disk: remove orphans
	dbRecords, err := sr.db.GetAllSegments(cameraName)
	if err != nil {
		slog.Error("failed to query segments from database", "camera", cameraName, "error", err)
		return
	}

	dbPaths := make(map[string]bool, len(dbRecords))
	for _, rec := range dbRecords {
		dbPaths[rec.Path] = true
		if _, exists := diskFiles[rec.Path]; !exists {
			slog.Warn("removing orphaned segment record", "camera", cameraName, "path", rec.Path)
			if err := sr.db.DeleteSegment(rec.Path); err != nil {
				slog.Error("failed to delete orphaned segment", "path", rec.Path, "error", err)
			}
		}
	}

	// Check disk files against DB: insert missing
	for path, info := range diskFiles {
		if dbPaths[path] {
			continue
		}

		duration := probeDuration(path)
		startTime := info.ModTime().Add(-duration)

		rec := storage.SegmentRecord{
			Camera:    cameraName,
			Path:      path,
			StartTime: startTime,
			EndTime:   info.ModTime(),
			SizeBytes: info.Size(),
		}

		slog.Info("importing segment from disk", "camera", cameraName, "path", path)
		if err := sr.db.SaveSegment(rec); err != nil {
			slog.Error("failed to import segment", "path", path, "error", err)
		}
	}
}

// probeDuration uses pure Go MP4 parsing to determine the duration of a video file.
func probeDuration(path string) time.Duration {
	dur, err := media.ProbeDuration(path)
	if err != nil {
		// Fall back to file modification time heuristic
		return 0
	}
	return dur
}

