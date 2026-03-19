package recording

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/storage"
)

// Segment represents a recorded video file covering a time range.
type Segment struct {
	Path      string
	Camera    string
	StartTime time.Time
	EndTime   time.Time
}

// SegmentRecorder continuously records RTSP streams into fixed-length segments.
type SegmentRecorder struct {
	config  config.RecordingConfig
	baseDir string
	db      *storage.DB
}

func NewSegmentRecorder(cfg config.RecordingConfig, db *storage.DB) *SegmentRecorder {
	baseDir := cfg.Path
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	return &SegmentRecorder{
		config:  cfg,
		baseDir: baseDir,
		db:      db,
	}
}

// StartRecording begins continuous segment recording for a camera.
func (sr *SegmentRecorder) StartRecording(ctx context.Context, cameraName, rtspURL string) {
	segDir := filepath.Join(sr.baseDir, cameraName, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		slog.Error("failed to create segment directory", "camera", cameraName, "error", err)
		return
	}

	go sr.recordLoop(ctx, cameraName, rtspURL, segDir)
}

func (sr *SegmentRecorder) recordLoop(ctx context.Context, cameraName, rtspURL, segDir string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Ensure segment directory exists before each recording attempt
		if err := os.MkdirAll(segDir, 0o755); err != nil {
			slog.Error("failed to ensure segment directory", "dir", segDir, "error", err)
		}

		startTime := time.Now()
		segPath := filepath.Join(segDir, fmt.Sprintf("%s.mp4", startTime.Format("2006-01-02_15-04-05")))

		duration := sr.config.SegmentLength
		if duration == 0 {
			duration = 10 * time.Minute
		}

		slog.Debug("starting segment", "camera", cameraName, "path", segPath)

		err := sr.recordSegment(ctx, rtspURL, segPath, duration)

		endTime := time.Now()

		// Register the segment even if ffmpeg exited early (partial segment)
		if info, statErr := os.Stat(segPath); statErr == nil {
			rec := storage.SegmentRecord{
				Camera:    cameraName,
				Path:      segPath,
				StartTime: startTime,
				EndTime:   endTime,
				SizeBytes: info.Size(),
			}

			if dbErr := sr.db.SaveSegment(rec); dbErr != nil {
				slog.Error("failed to save segment to database", "path", segPath, "error", dbErr)
			}

			slog.Debug("segment completed", "camera", cameraName, "path", segPath,
				"duration", endTime.Sub(startTime).Round(time.Second))
		}

		if err != nil {
			slog.Error("segment recording error, retrying",
				"camera", cameraName,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (sr *SegmentRecorder) recordSegment(ctx context.Context, rtspURL, outputPath string, duration time.Duration) error {
	segCtx, cancel := context.WithTimeout(ctx, duration+30*time.Second)
	defer cancel()

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-use_wallclock_as_timestamps", "1",
	}
	// Note: hwaccel args are intentionally omitted here.
	// Segment recording uses -c copy (remuxing), so no decoding occurs.
	args = append(args,
		"-i", rtspURL,
		"-t", fmt.Sprintf("%.0f", duration.Seconds()),
		"-c:v", "copy",
		"-c:a", "aac",
		"-movflags", "frag_keyframe+empty_moov",
		"-y",
		outputPath,
	)

	cmd := exec.CommandContext(segCtx, "ffmpeg", args...)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg segment: %w: %s", err, string(output))
	}

	return nil
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
// It inserts any .mp4 files found on disk but missing from the DB, and removes
// any DB records whose files no longer exist on disk.
func (sr *SegmentRecorder) ScanExistingSegments(cameraName, segDir string) {
	slog.Info("scanning existing segments", "camera", cameraName, "dir", segDir)

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

// probeDuration uses ffprobe to determine the duration of a video file.
func probeDuration(path string) time.Duration {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0
	}

	var seconds float64
	if _, err := fmt.Sscanf(result.Format.Duration, "%f", &seconds); err != nil {
		return 0
	}

	return time.Duration(seconds * float64(time.Second))
}
