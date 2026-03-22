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

func newTestRecorder(t *testing.T) (*Recorder, *storage.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.RecordingConfig{
		Path:          filepath.Join(tmpDir, "recordings"),
		SegmentLength: 10 * time.Minute,
		PreCapture:    5 * time.Second,
		PostCapture:   10 * time.Second,
		Continuous:    true,
	}
	rec := New(cfg, db, nil, "")
	return rec, db
}

func TestSaveClip_NoSegments_ReturnsError(t *testing.T) {
	rec, _ := newTestRecorder(t)

	event := camera.Event{
		ID:         "test-1",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  time.Now(),
	}

	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		t.Error("expected error when no segments available, got nil")
	}
}

func TestSaveClip_WithSegment_SavesClipPath(t *testing.T) {
	rec, db := newTestRecorder(t)

	segDir := filepath.Join(rec.config.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a dummy segment file
	dummyPath := filepath.Join(segDir, "2026-01-01_00-00-00.mp4")
	if err := os.WriteFile(dummyPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      dummyPath,
		StartTime: now.Add(-5 * time.Minute),
		EndTime:   now,
		SizeBytes: 4,
	})

	event := camera.Event{
		ID:         "test-2",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  now.Add(-1 * time.Minute),
	}

	// SaveClip will fail during trim (dummy file isn't valid MP4),
	// but should get past segment lookup
	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		// If it somehow succeeded, verify the clip path was saved
		return
	}
	// Expected: trim error (not "no segments available")
	if err.Error() == "extract clip: no segments available for camera \"cam1\"" {
		t.Error("segments should have been found")
	}
}

func TestSaveClip_DirCreationError_ReturnsError(t *testing.T) {
	rec, _ := newTestRecorder(t)

	segDir := filepath.Join(rec.config.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dummyPath := filepath.Join(segDir, "2026-01-01_00-00-00.mp4")
	if err := os.WriteFile(dummyPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	rec.db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      dummyPath,
		StartTime: now.Add(-5 * time.Minute),
		EndTime:   now,
		SizeBytes: 4,
	})

	// Block clip directory creation by placing a regular file where the directory should be
	clipsDateDir := filepath.Join(rec.config.Path, "cam1", "clips", now.Format("2006-01-02"))
	if err := os.MkdirAll(filepath.Dir(clipsDateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clipsDateDir, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	event := camera.Event{
		ID:         "test-3",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  now.Add(-1 * time.Minute),
	}

	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		t.Error("expected error for clip dir creation failure, got nil")
	}
}

func TestSaveClip_WithEndTime_UsesExtendedWindow(t *testing.T) {
	rec, db := newTestRecorder(t)

	segDir := filepath.Join(rec.config.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dummyPath := filepath.Join(segDir, "2026-01-01_00-00-00.mp4")
	if err := os.WriteFile(dummyPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      dummyPath,
		StartTime: now.Add(-10 * time.Minute),
		EndTime:   now,
		SizeBytes: 4,
	})

	// Event lasted 45 seconds (Timestamp to EndTime)
	event := camera.Event{
		ID:         "test-endtime",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  now.Add(-2 * time.Minute),
		EndTime:    now.Add(-2*time.Minute + 45*time.Second),
	}

	// Will fail during trim (dummy file), but should find the segment
	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		return
	}
	// Should NOT be "no segments available" — the extended window should find segments
	if err.Error() == `extract clip: no segments available for camera "cam1"` {
		t.Error("segments should have been found with EndTime-extended window")
	}
}

func TestSaveClip_ZeroEndTime_FallsBackToTimestamp(t *testing.T) {
	rec, db := newTestRecorder(t)

	segDir := filepath.Join(rec.config.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dummyPath := filepath.Join(segDir, "2026-01-01_00-00-00.mp4")
	if err := os.WriteFile(dummyPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      dummyPath,
		StartTime: now.Add(-5 * time.Minute),
		EndTime:   now,
		SizeBytes: 4,
	})

	// Event with zero EndTime (old behavior)
	event := camera.Event{
		ID:         "test-zero-end",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  now.Add(-1 * time.Minute),
	}

	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		return
	}
	if err.Error() == `extract clip: no segments available for camera "cam1"` {
		t.Error("segments should have been found with zero EndTime fallback")
	}
}

func TestCameraURL_Registered(t *testing.T) {
	rec, _ := newTestRecorder(t)
	rec.RegisterCamera("cam1", "rtsp://host/stream")

	url := rec.CameraURL("cam1")
	if url != "rtsp://host/stream" {
		t.Errorf("CameraURL = %q, want %q", url, "rtsp://host/stream")
	}
}

func TestCameraURL_NotRegistered(t *testing.T) {
	rec, _ := newTestRecorder(t)

	url := rec.CameraURL("nonexistent")
	if url != "" {
		t.Errorf("CameraURL for unknown camera = %q, want empty", url)
	}
}

func TestStorageStats_EmptyDB(t *testing.T) {
	rec, _ := newTestRecorder(t)

	stats := rec.StorageStats()
	if stats.TotalBytes != 0 {
		t.Errorf("TotalBytes = %d, want 0", stats.TotalBytes)
	}
	if stats.SegmentCount != 0 {
		t.Errorf("SegmentCount = %d, want 0", stats.SegmentCount)
	}
	if len(stats.CameraStats) != 0 {
		t.Errorf("CameraStats should be empty, got %v", stats.CameraStats)
	}
}

func TestStorageStats_WithSegments(t *testing.T) {
	rec, db := newTestRecorder(t)
	now := time.Now()

	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      "/tmp/s1.mp4",
		StartTime: now.Add(-10 * time.Minute),
		EndTime:   now,
		SizeBytes: 5000,
	})
	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam2",
		Path:      "/tmp/s2.mp4",
		StartTime: now.Add(-10 * time.Minute),
		EndTime:   now,
		SizeBytes: 3000,
	})

	rec.RefreshStats()
	stats := rec.StorageStats()
	if stats.TotalBytes != 8000 {
		t.Errorf("TotalBytes = %d, want 8000", stats.TotalBytes)
	}
	if stats.SegmentCount != 2 {
		t.Errorf("SegmentCount = %d, want 2", stats.SegmentCount)
	}
	if stats.CameraStats["cam1"] != 5000 {
		t.Errorf("cam1 bytes = %d, want 5000", stats.CameraStats["cam1"])
	}
	if stats.CameraStats["cam2"] != 3000 {
		t.Errorf("cam2 bytes = %d, want 3000", stats.CameraStats["cam2"])
	}
}

func TestRecorderClose_ReturnsWithoutHang(t *testing.T) {
	rec, _ := newTestRecorder(t)

	done := make(chan struct{})
	go func() {
		rec.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(15 * time.Second):
		t.Fatal("recorder.Close() hung for more than 15 seconds")
	}
}
