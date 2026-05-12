package recording

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/snapshot"
)

func TestSaveEventSnapshot_WritesAndUpdatesRow(t *testing.T) {
	tmp := t.TempDir()
	_, db := newTestRecorder(t)

	saver := snapshot.NewSaver(
		filepath.Join(tmp, "snaps"),
		filepath.Join(tmp, "fallback"),
		85,
	)

	ev := camera.Event{
		ID:         "evt-1",
		CameraName: "cam-a",
		Label:      "person",
		Score:      0.9,
		Timestamp:  time.Now(),
	}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatal(err)
	}

	r := &Recorder{db: db, snapshotSaver: saver}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	primary := filepath.Join(tmp, "snaps", "cam-a", "evt-1.jpg")

	resolved, err := r.SaveEventSnapshot(ev, img, primary)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != primary {
		t.Errorf("resolved=%q, want %q", resolved, primary)
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Fatalf("expected file at %q: %v", resolved, err)
	}

	got, err := db.GetEventByID("evt-1")
	if err != nil || got == nil {
		t.Fatal("event not found after save")
	}
	if got.SnapshotPath != primary {
		t.Errorf("snapshot_path = %q, want %q", got.SnapshotPath, primary)
	}
	if !got.SnapshotAvailable {
		t.Errorf("SnapshotAvailable not set true")
	}
}

func TestSaveEventSnapshot_NilSaver_ReturnsError(t *testing.T) {
	_, db := newTestRecorder(t)
	r := &Recorder{db: db, snapshotSaver: nil}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))

	_, err := r.SaveEventSnapshot(camera.Event{ID: "x"}, img, "/tmp/x.jpg")
	if err == nil {
		t.Error("expected error when snapshotSaver is nil, got nil")
	}
}
