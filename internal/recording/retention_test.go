package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/storage"
)

// TestCleanSegments_RemovesOrphanCameraRows verifies that retention cleanup
// deletes expired segment rows from cameras that no longer exist in the
// filesystem — i.e., cameras that were removed from the config but whose
// DB rows were never cleaned up.
//
// Regression test for a bug where cleanSegments iterated filesystem
// directories via listCameras(), so segments belonging to a deleted camera
// directory were invisible to cleanup and lived in the DB forever.
func TestCleanSegments_RemovesOrphanCameraRows(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 7

	// Seed an expired segment for a camera whose filesystem directory does
	// NOT exist (simulating a camera that was removed from the config).
	oldPath := filepath.Join(rec.config.Path, "deleted_camera", "segments", "old.mp4")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("expired"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera:    "deleted_camera",
		Path:      oldPath,
		StartTime: time.Now().Add(-20 * 24 * time.Hour),
		EndTime:   time.Now().Add(-20 * 24 * time.Hour).Add(10 * time.Minute),
		SizeBytes: 7,
	}); err != nil {
		t.Fatal(err)
	}

	// Remove the filesystem directory to simulate the camera being gone —
	// the old file-based listCameras() would return nothing, missing this
	// segment entirely.
	if err := os.RemoveAll(filepath.Join(rec.config.Path, "deleted_camera")); err != nil {
		t.Fatal(err)
	}
	// Restore just the file so RemoveSegment's os.Remove call is a no-op
	// (simulating the real scenario where the file is also gone).

	// Also seed a current segment for an active camera to make sure it stays.
	activeSegDir := filepath.Join(rec.config.Path, "active_camera", "segments")
	if err := os.MkdirAll(activeSegDir, 0o755); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(activeSegDir, "fresh.mp4")
	if err := os.WriteFile(activePath, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera:    "active_camera",
		Path:      activePath,
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		SizeBytes: 5,
	}); err != nil {
		t.Fatal(err)
	}

	// Run the cleanup.
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	rec.cleanSegments(cutoff)

	// The orphan segment should be gone from the DB.
	if existing, _ := db.GetSegmentByPath(oldPath); existing != nil {
		t.Errorf("expected orphan segment to be deleted from DB, but it still exists")
	}

	// The active segment should still be present.
	if existing, _ := db.GetSegmentByPath(activePath); existing == nil {
		t.Error("expected active segment to remain, but it was deleted")
	}
}

// TestCleanSegments_KeepsRecentSegments ensures cleanup doesn't remove
// segments within the retention window.
func TestCleanSegments_KeepsRecentSegments(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 7

	segDir := filepath.Join(rec.config.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recentPath := filepath.Join(segDir, "recent.mp4")
	if err := os.WriteFile(recentPath, []byte("recent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      recentPath,
		StartTime: time.Now().Add(-3 * 24 * time.Hour),
		EndTime:   time.Now().Add(-3 * 24 * time.Hour).Add(10 * time.Minute),
		SizeBytes: 6,
	}); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	rec.cleanSegments(cutoff)

	if existing, _ := db.GetSegmentByPath(recentPath); existing == nil {
		t.Error("expected recent segment to remain, but it was deleted")
	}
}
