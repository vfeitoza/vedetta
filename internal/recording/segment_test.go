package recording

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/storage"
)

func newTestSegmentRecorder(t *testing.T) *SegmentRecorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSegmentRecorder(config.RecordingConfig{}, db)
}

func seedSegments(t *testing.T, sr *SegmentRecorder, segments []storage.SegmentRecord) {
	t.Helper()
	for _, seg := range segments {
		if err := sr.db.SaveSegment(seg); err != nil {
			t.Fatalf("failed to seed segment: %v", err)
		}
	}
}

func TestSegmentRecorder_FindSegments(t *testing.T) {
	sr := newTestSegmentRecorder(t)
	now := time.Now().Truncate(time.Second)

	seedSegments(t, sr, []storage.SegmentRecord{
		{Camera: "cam1", Path: "/seg1.mp4", StartTime: now.Add(-30 * time.Minute), EndTime: now.Add(-20 * time.Minute)},
		{Camera: "cam1", Path: "/seg2.mp4", StartTime: now.Add(-20 * time.Minute), EndTime: now.Add(-10 * time.Minute)},
		{Camera: "cam1", Path: "/seg3.mp4", StartTime: now.Add(-10 * time.Minute), EndTime: now},
	})

	from := now.Add(-15 * time.Minute)
	to := now.Add(-5 * time.Minute)
	result := sr.FindSegments("cam1", from, to)

	if len(result) != 2 {
		t.Errorf("expected 2 overlapping segments, got %d", len(result))
	}
}

func TestSegmentRecorder_FindSegments_NoMatch(t *testing.T) {
	sr := newTestSegmentRecorder(t)
	now := time.Now().Truncate(time.Second)

	seedSegments(t, sr, []storage.SegmentRecord{
		{Camera: "cam1", Path: "/seg1.mp4", StartTime: now.Add(-30 * time.Minute), EndTime: now.Add(-20 * time.Minute)},
	})

	from := now.Add(-5 * time.Minute)
	to := now
	result := sr.FindSegments("cam1", from, to)

	if len(result) != 0 {
		t.Errorf("expected 0 segments, got %d", len(result))
	}
}

func TestSegmentRecorder_FindSegments_UnknownCamera(t *testing.T) {
	sr := newTestSegmentRecorder(t)

	result := sr.FindSegments("nonexistent", time.Now().Add(-time.Hour), time.Now())

	if len(result) != 0 {
		t.Errorf("expected 0 segments for unknown camera, got %d", len(result))
	}
}

func TestSegmentRecorder_RemoveSegment(t *testing.T) {
	sr := newTestSegmentRecorder(t)
	now := time.Now().Truncate(time.Second)

	seedSegments(t, sr, []storage.SegmentRecord{
		{Camera: "cam1", Path: "/seg1.mp4", StartTime: now.Add(-20 * time.Minute), EndTime: now.Add(-10 * time.Minute)},
		{Camera: "cam1", Path: "/seg2.mp4", StartTime: now.Add(-10 * time.Minute), EndTime: now},
	})

	_ = sr.RemoveSegment("cam1", "/seg1.mp4")

	segs := sr.AllSegments("cam1")
	if len(segs) != 1 {
		t.Errorf("expected 1 segment after removal, got %d", len(segs))
	}
	if segs[0].Path != "/seg2.mp4" {
		t.Errorf("expected remaining segment /seg2.mp4, got %s", segs[0].Path)
	}
}
