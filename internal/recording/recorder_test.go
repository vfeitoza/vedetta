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

func TestSaveClip_StartupGracePeriod_SuppressesNoSegments(t *testing.T) {
	rec, _ := newTestRecorder(t)

	event := camera.Event{
		ID:         "test-1",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  time.Now(),
	}

	// During startup (within segment length), "no segments available" should be suppressed
	err := rec.SaveClip(context.Background(), event)
	if err != nil {
		t.Errorf("expected no error during startup grace period, got: %v", err)
	}
}

func TestSaveClip_StartupGracePeriod_DoesNotSuppressOtherErrors(t *testing.T) {
	rec, _ := newTestRecorder(t)

	// Create a segment record that exists on disk so FindSegments returns it,
	// but make the clip output directory a read-only file so clip creation fails.
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
	// Create a file where MkdirAll expects to create a directory
	if err := os.WriteFile(clipsDateDir, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	event := camera.Event{
		ID:         "test-2",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  now.Add(-1 * time.Minute),
	}

	// This should NOT be suppressed because it's a directory creation error, not "no segments"
	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		t.Error("expected error for clip dir creation failure during startup, got nil")
	}
}

func TestSaveClip_AfterGracePeriod_ReturnsError(t *testing.T) {
	rec, _ := newTestRecorder(t)
	// Set startTime to the past so grace period has expired
	rec.startTime = time.Now().Add(-20 * time.Minute)

	event := camera.Event{
		ID:         "test-3",
		CameraName: "cam1",
		Label:      "car",
		Timestamp:  time.Now(),
	}

	err := rec.SaveClip(context.Background(), event)
	if err == nil {
		t.Error("expected error after grace period with no segments, got nil")
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
