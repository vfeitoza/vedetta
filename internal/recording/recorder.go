package recording

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/rvben/watchpost/internal/camera"
	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/storage"
)

// Recorder manages saving video clips for detected events.
type Recorder struct {
	config     config.RecordingConfig
	db         *storage.DB
	segments   *SegmentRecorder
	cameraURLs map[string]string // camera name → record RTSP URL
}

func New(cfg config.RecordingConfig, db *storage.DB) *Recorder {
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		slog.Error("failed to create recording directory", "path", cfg.Path, "error", err)
	}

	return &Recorder{
		config:     cfg,
		db:         db,
		segments:   NewSegmentRecorder(cfg),
		cameraURLs: make(map[string]string),
	}
}

// RegisterCamera registers a camera's recording URL for direct-from-stream recording.
func (r *Recorder) RegisterCamera(name, rtspURL string) {
	r.cameraURLs[name] = rtspURL
}

// StartContinuousRecording begins segment recording for all registered cameras.
func (r *Recorder) StartContinuousRecording(ctx context.Context) {
	if !r.config.Continuous {
		slog.Info("continuous recording disabled")
		return
	}

	for name, url := range r.cameraURLs {
		r.segments.StartRecording(ctx, name, url)
	}

	slog.Info("continuous recording started", "cameras", len(r.cameraURLs))
}

// SaveClip records a clip around the event timestamp.
// It first tries to extract from existing segments, then falls back to direct recording.
func (r *Recorder) SaveClip(ctx context.Context, event camera.Event) error {
	clipPath, err := r.ExtractClip(ctx, event)
	if err != nil {
		return fmt.Errorf("extract clip: %w", err)
	}

	// Update the event with the clip path
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

// recordFromStream uses ffmpeg to capture a clip directly from RTSP.
func (r *Recorder) recordFromStream(ctx context.Context, rtspURL, outputPath string, duration time.Duration) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-t", fmt.Sprintf("%.0f", duration.Seconds()),
		"-c", "copy",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg record: %w: %s", err, string(output))
	}

	return nil
}
