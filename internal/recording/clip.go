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

// ClipStats describes the work a clip extraction represented. It is reported
// alongside the result (and the error, populated as far as extraction got) so
// the clip.extract span can explain its latency: a many-segment concat or a
// long window costs more than a single short trim.
type ClipStats struct {
	SegmentCount int           // segments stitched into the clip
	ClipDuration time.Duration // pre+event+post window length
	OutputBytes  int64         // size of the written clip; 0 unless extraction succeeded
}

// ExtractClip creates an event clip by copying relevant segments
// and trimming to the event's pre/post capture window. The returned ClipStats
// is populated up to the point extraction reached, so a partial failure (e.g. a
// trim error) still reports the segment count and window that were resolved.
func (r *Recorder) ExtractClip(ctx context.Context, event camera.Event) (string, ClipStats, error) {
	var stats ClipStats
	if err := ctx.Err(); err != nil {
		return "", stats, err
	}
	clipDir, err := safepath.Join(r.config.Path, event.CameraName, "clips", event.Timestamp.Format("2006-01-02"))
	if err != nil {
		return "", stats, fmt.Errorf("resolve clip dir: %w", err)
	}
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		return "", stats, fmt.Errorf("create clip dir: %w", err)
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
	stats.ClipDuration = to.Sub(from)

	segments := r.segments.FindSegments(event.CameraName, from, to)

	if len(segments) == 0 {
		return "", stats, fmt.Errorf("no segments available for camera %q", event.CameraName)
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
		return "", stats, fmt.Errorf("segments deleted before clip extraction for camera %q", event.CameraName)
	}
	stats.SegmentCount = len(segments)

	startOffset := from.Sub(segments[0].StartTime)
	if startOffset < 0 {
		startOffset = 0
	}
	duration := stats.ClipDuration

	// Abort before the expensive trim/concat if shutdown began while we were
	// resolving segments.
	if err := ctx.Err(); err != nil {
		return "", stats, err
	}

	if len(segments) == 1 {
		if err := media.TrimMP4(segments[0].Path, clipPath, startOffset, duration); err != nil {
			return "", stats, fmt.Errorf("trim segment: %w", err)
		}
	} else {
		// Multiple segments — concat then trim
		inputs := make([]string, len(segments))
		for i, seg := range segments {
			inputs[i] = seg.Path
		}
		if err := media.ConcatMP4(inputs, clipPath, startOffset, duration); err != nil {
			return "", stats, fmt.Errorf("concat and trim: %w", err)
		}
	}

	if fi, err := os.Stat(clipPath); err == nil {
		stats.OutputBytes = fi.Size()
	}
	return clipPath, stats, nil
}

func formatDuration(d time.Duration) string {
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	ms := d.Milliseconds() % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
