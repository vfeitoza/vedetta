package recording

import (
	"testing"
	"time"

	"github.com/rvben/watchpost/internal/config"
)

func TestSegmentRecorder_FindSegments(t *testing.T) {
	sr := NewSegmentRecorder(config.RecordingConfig{})

	now := time.Now()

	// Add 3 segments: 10min each
	sr.segments["cam1"] = []Segment{
		{Path: "/seg1.mp4", Camera: "cam1", StartTime: now.Add(-30 * time.Minute), EndTime: now.Add(-20 * time.Minute)},
		{Path: "/seg2.mp4", Camera: "cam1", StartTime: now.Add(-20 * time.Minute), EndTime: now.Add(-10 * time.Minute)},
		{Path: "/seg3.mp4", Camera: "cam1", StartTime: now.Add(-10 * time.Minute), EndTime: now},
	}

	// Query a range that overlaps seg2 and seg3
	from := now.Add(-15 * time.Minute)
	to := now.Add(-5 * time.Minute)
	result := sr.FindSegments("cam1", from, to)

	if len(result) != 2 {
		t.Errorf("expected 2 overlapping segments, got %d", len(result))
	}
}

func TestSegmentRecorder_FindSegments_NoMatch(t *testing.T) {
	sr := NewSegmentRecorder(config.RecordingConfig{})

	now := time.Now()
	sr.segments["cam1"] = []Segment{
		{Path: "/seg1.mp4", Camera: "cam1", StartTime: now.Add(-30 * time.Minute), EndTime: now.Add(-20 * time.Minute)},
	}

	// Query a range entirely after the segment
	from := now.Add(-5 * time.Minute)
	to := now
	result := sr.FindSegments("cam1", from, to)

	if len(result) != 0 {
		t.Errorf("expected 0 segments, got %d", len(result))
	}
}

func TestSegmentRecorder_FindSegments_UnknownCamera(t *testing.T) {
	sr := NewSegmentRecorder(config.RecordingConfig{})

	result := sr.FindSegments("nonexistent", time.Now().Add(-time.Hour), time.Now())

	if len(result) != 0 {
		t.Errorf("expected 0 segments for unknown camera, got %d", len(result))
	}
}

func TestSegmentRecorder_RemoveSegment(t *testing.T) {
	sr := NewSegmentRecorder(config.RecordingConfig{})

	now := time.Now()
	sr.segments["cam1"] = []Segment{
		{Path: "/seg1.mp4", Camera: "cam1", StartTime: now.Add(-20 * time.Minute), EndTime: now.Add(-10 * time.Minute)},
		{Path: "/seg2.mp4", Camera: "cam1", StartTime: now.Add(-10 * time.Minute), EndTime: now},
	}

	// Remove a non-existent file path (the os.Remove will fail silently)
	_ = sr.RemoveSegment("cam1", "/seg1.mp4")

	segs := sr.AllSegments("cam1")
	if len(segs) != 1 {
		t.Errorf("expected 1 segment after removal, got %d", len(segs))
	}
	if segs[0].Path != "/seg2.mp4" {
		t.Errorf("expected remaining segment /seg2.mp4, got %s", segs[0].Path)
	}
}
