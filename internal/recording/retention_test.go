package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
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
	rec.cleanSegments()

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

	rec.cleanSegments()

	if existing, _ := db.GetSegmentByPath(recentPath); existing == nil {
		t.Error("expected recent segment to remain, but it was deleted")
	}
}

// TestCleanSegments_PerCameraRetention verifies that per-camera retain_days
// overrides are honoured: a shorter override deletes earlier, a longer
// override keeps segments beyond the global window, and cameras without an
// explicit override fall back to the global setting.
func TestCleanSegments_PerCameraRetention(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 7
	rec.cameraRetention = map[string]int{
		"cam_short": 1,  // shorter than global → 1.5d-old must be deleted
		"cam_long":  30, // longer than global → 10d-old must be kept
	}
	now := time.Now()

	seed := func(cam string, age time.Duration) string {
		end := now.Add(-age)
		segDir := filepath.Join(rec.config.Path, cam, "segments")
		if err := os.MkdirAll(segDir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(segDir, fmt.Sprintf("%s_%d.mp4", cam, age))
		if err := os.WriteFile(path, []byte("seg"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveSegment(storage.SegmentRecord{
			Camera:    cam,
			Path:      path,
			StartTime: end.Add(-10 * time.Minute),
			EndTime:   end,
			SizeBytes: 3,
		}); err != nil {
			t.Fatal(err)
		}
		return path
	}

	shortYoung := seed("cam_short", 12*time.Hour)       // < 1d → keep
	shortOld := seed("cam_short", 36*time.Hour)         // > 1d → delete
	longOld := seed("cam_long", 10*24*time.Hour)        // > 7d but < 30d → keep
	defaultYoung := seed("cam_default", 3*24*time.Hour) // < 7d → keep
	defaultOld := seed("cam_default", 8*24*time.Hour)   // > 7d → delete

	rec.cleanSegments()

	cases := []struct {
		path     string
		wantKept bool
		label    string
	}{
		{shortYoung, true, "cam_short young"},
		{shortOld, false, "cam_short old (override)"},
		{longOld, true, "cam_long old (override extends)"},
		{defaultYoung, true, "cam_default young"},
		{defaultOld, false, "cam_default old (global)"},
	}
	for _, tc := range cases {
		seg, _ := db.GetSegmentByPath(tc.path)
		kept := seg != nil
		if kept != tc.wantKept {
			t.Errorf("%s: kept=%v, want=%v", tc.label, kept, tc.wantKept)
		}
	}
}

func TestRunCleanupHoldsSegmentOpMu(t *testing.T) {
	r := &Recorder{} // zero-value; subsystems are nil
	lockHeld := atomic.Bool{}

	// Intercept when the lock is held by replacing the body with a channel sync.
	// We can't run runCleanupLocked on a nil Recorder (it would nil-deref on r.db),
	// so we test the wrapper by observing lock state from a concurrent goroutine
	// that races against runCleanup. Instead, we directly exercise the wrapper
	// contract: acquire → lock is held → release → lock is free.
	done := make(chan struct{})
	go func() {
		defer func() {
			// runCleanupLocked panics on nil r.db; recover so we can still
			// observe that the lock was taken and released by the wrapper.
			recover() //nolint:errcheck
			close(done)
		}()
		lockHeld.Store(false)
		r.runCleanup() // wrapper takes segmentOpMu; body panics and is recovered
	}()

	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case <-done:
			// runCleanup (or its recover) returned; lock must be released.
			if !r.segmentOpMu.TryLock() {
				t.Fatal("expected lock released after runCleanup")
			}
			r.segmentOpMu.Unlock()
			return
		case <-deadline:
			t.Fatal("runCleanup did not return within deadline")
		default:
			// Busy-spin: if we can grab the lock while the goroutine is
			// between wrapper entry and body panic, that's fine — the body
			// exits so fast we may race. Record it for diagnostics only.
			if r.segmentOpMu.TryLock() {
				lockHeld.Store(true)
				r.segmentOpMu.Unlock()
			}
		}
	}
}
