package recording

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rvben/watchpost/internal/camera"
)

// ExtractClip creates an event clip by concatenating relevant segments
// and trimming to the event's pre/post capture window.
func (r *Recorder) ExtractClip(ctx context.Context, event camera.Event) (string, error) {
	clipDir := filepath.Join(r.config.Path, event.CameraName, "clips", event.Timestamp.Format("2006-01-02"))
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		return "", fmt.Errorf("create clip dir: %w", err)
	}

	filename := fmt.Sprintf("%s_%s_%s.mp4",
		event.Timestamp.Format("15-04-05"),
		event.Label,
		event.ID,
	)
	clipPath := filepath.Join(clipDir, filename)

	from := event.Timestamp.Add(-r.config.PreCapture)
	to := event.Timestamp.Add(r.config.PostCapture)

	segments := r.segments.FindSegments(event.CameraName, from, to)

	if len(segments) == 0 {
		// No segments available — record directly from stream
		slog.Warn("no segments available, recording from stream",
			"camera", event.CameraName,
		)
		duration := r.config.PreCapture + r.config.PostCapture
		if duration == 0 {
			duration = 15 * time.Second
		}
		rtspURL := r.cameraURLs[event.CameraName]
		if rtspURL == "" {
			return "", fmt.Errorf("no stream URL for camera %q", event.CameraName)
		}
		if err := r.recordFromStream(ctx, rtspURL, clipPath, duration); err != nil {
			return "", err
		}
		return clipPath, nil
	}

	// Filter out segments whose files no longer exist (retention may have deleted them).
	valid := segments[:0]
	for _, seg := range segments {
		if _, err := os.Stat(seg.Path); err == nil {
			valid = append(valid, seg)
		}
	}
	segments = valid

	if len(segments) == 0 {
		// Segments were deleted between query and access — fall back to stream
		slog.Warn("segments deleted before clip extraction, recording from stream",
			"camera", event.CameraName,
		)
		duration := r.config.PreCapture + r.config.PostCapture
		if duration == 0 {
			duration = 15 * time.Second
		}
		rtspURL := r.cameraURLs[event.CameraName]
		if rtspURL == "" {
			return "", fmt.Errorf("no stream URL for camera %q", event.CameraName)
		}
		if err := r.recordFromStream(ctx, rtspURL, clipPath, duration); err != nil {
			return "", err
		}
		return clipPath, nil
	}

	startOffset := from.Sub(segments[0].StartTime)
	if startOffset < 0 {
		startOffset = 0
	}
	duration := to.Sub(from)

	if len(segments) == 1 {
		// Single segment — trim it directly
		if err := trimSegment(ctx, segments[0].Path, clipPath, startOffset, duration); err != nil {
			return "", fmt.Errorf("trim segment: %w", err)
		}
		return clipPath, nil
	}

	// Multiple segments — concat+trim in a single ffmpeg pass
	if err := concatAndTrim(ctx, segments, clipPath, startOffset, duration); err != nil {
		return "", fmt.Errorf("concat and trim: %w", err)
	}

	return clipPath, nil
}

// trimSegment extracts a portion of a video file using ffmpeg.
func trimSegment(ctx context.Context, inputPath, outputPath string, startOffset, duration time.Duration) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-ss", formatDuration(startOffset),
		"-i", inputPath,
		"-t", formatDuration(duration),
		"-c", "copy",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg trim: %w: %s", err, string(output))
	}

	return nil
}

// concatAndTrim concatenates multiple segments and trims to the desired window
// in a single ffmpeg pass, avoiding an intermediate concatenated file.
func concatAndTrim(ctx context.Context, segments []Segment, outputPath string, startOffset, duration time.Duration) error {
	listPath := outputPath + ".txt"
	var lines []string
	for _, seg := range segments {
		escaped := strings.ReplaceAll(seg.Path, "'", "'\\''")
		lines = append(lines, fmt.Sprintf("file '%s'", escaped))
	}

	if err := os.WriteFile(listPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write concat list: %w", err)
	}
	defer os.Remove(listPath)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-ss", formatDuration(startOffset),
		"-t", formatDuration(duration),
		"-c", "copy",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg concat+trim: %w: %s", err, string(output))
	}

	return nil
}

func formatDuration(d time.Duration) string {
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	ms := d.Milliseconds() % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
