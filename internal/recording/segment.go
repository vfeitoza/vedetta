package recording

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/rvben/watchpost/internal/config"
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

	mu       sync.RWMutex
	segments map[string][]Segment // camera name → segments (chronological)
}

func NewSegmentRecorder(cfg config.RecordingConfig) *SegmentRecorder {
	return &SegmentRecorder{
		config:   cfg,
		baseDir:  cfg.Path,
		segments: make(map[string][]Segment),
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
		if _, statErr := os.Stat(segPath); statErr == nil {
			seg := Segment{
				Path:      segPath,
				Camera:    cameraName,
				StartTime: startTime,
				EndTime:   endTime,
			}

			sr.mu.Lock()
			sr.segments[cameraName] = append(sr.segments[cameraName], seg)
			sr.mu.Unlock()

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

	cmd := exec.CommandContext(segCtx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-use_wallclock_as_timestamps", "1",
		"-i", rtspURL,
		"-t", fmt.Sprintf("%.0f", duration.Seconds()),
		"-c", "copy",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg segment: %w: %s", err, string(output))
	}

	return nil
}

// FindSegments returns segments for a camera that overlap the given time range.
func (sr *SegmentRecorder) FindSegments(cameraName string, from, to time.Time) []Segment {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	var result []Segment
	for _, seg := range sr.segments[cameraName] {
		// Segment overlaps if it starts before 'to' and ends after 'from'
		if seg.StartTime.Before(to) && seg.EndTime.After(from) {
			result = append(result, seg)
		}
	}
	return result
}

// RemoveSegment deletes a segment file and removes it from the index.
func (sr *SegmentRecorder) RemoveSegment(cameraName string, segPath string) error {
	if err := os.Remove(segPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	segs := sr.segments[cameraName]
	for i, s := range segs {
		if s.Path == segPath {
			sr.segments[cameraName] = append(segs[:i], segs[i+1:]...)
			break
		}
	}
	return nil
}

// AllSegments returns all segments for a given camera.
func (sr *SegmentRecorder) AllSegments(cameraName string) []Segment {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	result := make([]Segment, len(sr.segments[cameraName]))
	copy(result, sr.segments[cameraName])
	return result
}
