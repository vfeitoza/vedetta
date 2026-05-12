package recording

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

func TestExtractClip_DynamicWindow_WithEndTime(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.RecordingConfig{
		Path:        filepath.Join(tmpDir, "recordings"),
		PreCapture:  5 * time.Second,
		PostCapture: 10 * time.Second,
	}
	rec := New(cfg, config.EventConfig{RetainDays: 90}, nil, db, nil, "", "", nil)

	segDir := filepath.Join(cfg.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	segPath := filepath.Join(segDir, "seg.mp4")
	if err := os.WriteFile(segPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      segPath,
		StartTime: now.Add(-10 * time.Minute),
		EndTime:   now.Add(5 * time.Minute),
		SizeBytes: 4,
	})

	// Event with EndTime 90 seconds after start.
	// Clip should span: (Timestamp - 5s) to (EndTime + 10s) = 105 second window.
	event := camera.Event{
		ID:         "clip-dyn",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  now.Add(-2 * time.Minute),
		EndTime:    now.Add(-2*time.Minute + 90*time.Second),
	}

	// ExtractClip will fail on trim (not valid MP4), but verify it finds segments
	_, err = rec.ExtractClip(context.TODO(), event)
	if err != nil && err.Error() == `no segments available for camera "cam1"` {
		t.Error("dynamic window should find segments")
	}
}

func TestExtractClip_ZeroEndTime_UsesTimestampOnly(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.RecordingConfig{
		Path:        filepath.Join(tmpDir, "recordings"),
		PreCapture:  5 * time.Second,
		PostCapture: 10 * time.Second,
	}
	rec := New(cfg, config.EventConfig{RetainDays: 90}, nil, db, nil, "", "", nil)

	segDir := filepath.Join(cfg.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	segPath := filepath.Join(segDir, "seg.mp4")
	if err := os.WriteFile(segPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      segPath,
		StartTime: now.Add(-5 * time.Minute),
		EndTime:   now,
		SizeBytes: 4,
	})

	// Zero EndTime — should use Timestamp as EndTime fallback
	event := camera.Event{
		ID:         "clip-zero",
		CameraName: "cam1",
		Label:      "car",
		Timestamp:  now.Add(-1 * time.Minute),
	}

	_, err = rec.ExtractClip(context.TODO(), event)
	if err != nil && err.Error() == `no segments available for camera "cam1"` {
		t.Error("zero EndTime fallback should still find segments")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00:00.000"},
		{5 * time.Second, "00:00:05.000"},
		{90 * time.Second, "00:01:30.000"},
		{time.Hour + 30*time.Minute + 15*time.Second + 500*time.Millisecond, "01:30:15.500"},
		{2*time.Hour + 500*time.Millisecond, "02:00:00.500"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
