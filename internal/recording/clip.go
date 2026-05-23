package recording

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/safepath"
)

// ExtractClip creates an event clip by copying relevant segments
// and trimming to the event's pre/post capture window.
func (r *Recorder) ExtractClip(ctx context.Context, event camera.Event) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	clipDir, err := safepath.Join(r.config.Path, event.CameraName, "clips", event.Timestamp.Format("2006-01-02"))
	if err != nil {
		return "", fmt.Errorf("resolve clip dir: %w", err)
	}
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		return "", fmt.Errorf("create clip dir: %w", err)
	}

	filename := fmt.Sprintf("%s_%s_%s.mp4",
		event.Timestamp.Format("15-04-05"),
		safepath.FileComponent(event.Label),
		safepath.FileComponent(event.ID),
	)
	clipPath := filepath.Join(clipDir, filename)

	from := event.Timestamp.Add(-r.config.PreCapture)
	endTime := event.EndTime
	if endTime.IsZero() {
		endTime = event.Timestamp
	}
	to := endTime.Add(r.config.PostCapture)

	segments := r.segments.FindSegments(event.CameraName, from, to)

	if len(segments) == 0 {
		return "", fmt.Errorf("no segments available for camera %q", event.CameraName)
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
		return "", fmt.Errorf("segments deleted before clip extraction for camera %q", event.CameraName)
	}

	startOffset := from.Sub(segments[0].StartTime)
	if startOffset < 0 {
		startOffset = 0
	}
	duration := to.Sub(from)

	// Abort before the expensive trim/concat if shutdown began while we were
	// resolving segments.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if len(segments) == 1 {
		if err := media.TrimMP4(segments[0].Path, clipPath, startOffset, duration); err != nil {
			return "", fmt.Errorf("trim segment: %w", err)
		}
		return clipPath, nil
	}

	// Multiple segments — concat then trim
	inputs := make([]string, len(segments))
	for i, seg := range segments {
		inputs[i] = seg.Path
	}
	if err := media.ConcatMP4(inputs, clipPath, startOffset, duration); err != nil {
		return "", fmt.Errorf("concat and trim: %w", err)
	}

	return clipPath, nil
}

func formatDuration(d time.Duration) string {
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	ms := d.Milliseconds() % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
