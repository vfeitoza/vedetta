package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/storage"
)

// A retain value of 0 (or negative) means "keep forever". The bug these tests
// guard against is computing a cutoff of time.Now().Add(-0) == now, which makes
// every record older than the current instant expired and deletes everything.

// TestCleanSegments_RetainDaysZeroKeepsAll verifies an unlimited global
// retention (retain_days <= 0) does not delete old segments.
func TestCleanSegments_RetainDaysZeroKeepsAll(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 0 // unlimited

	segDir := filepath.Join(rec.config.Path, "cam1", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(segDir, "ancient.mp4")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      oldPath,
		StartTime: time.Now().Add(-100 * 24 * time.Hour),
		EndTime:   time.Now().Add(-100 * 24 * time.Hour).Add(10 * time.Minute),
		SizeBytes: 3,
	}); err != nil {
		t.Fatal(err)
	}

	rec.cleanSegments()

	if seg, _ := db.GetSegmentByPath(oldPath); seg == nil {
		t.Error("retain_days=0 must keep all segments, but the 100-day-old segment was deleted")
	}
}

// TestCleanSegments_RetainDaysZeroStillHonorsPerCameraOverride verifies that an
// unlimited global retention does not disable explicit per-camera overrides:
// a camera with retain_days=1 must still have its old segments deleted even
// though the global setting keeps everything forever.
func TestCleanSegments_RetainDaysZeroStillHonorsPerCameraOverride(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 0 // unlimited global
	rec.cameraRetention = map[string]int{
		"cam_short": 1, // explicit 1-day retention
	}
	now := time.Now()

	seed := func(cam, name string, age time.Duration) string {
		segDir := filepath.Join(rec.config.Path, cam, "segments")
		if err := os.MkdirAll(segDir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(segDir, name)
		if err := os.WriteFile(path, []byte("seg"), 0o644); err != nil {
			t.Fatal(err)
		}
		end := now.Add(-age)
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

	shortOld := seed("cam_short", "old.mp4", 2*24*time.Hour)       // > 1d override → delete
	defaultOld := seed("cam_default", "old.mp4", 100*24*time.Hour) // global unlimited → keep

	rec.cleanSegments()

	if seg, _ := db.GetSegmentByPath(shortOld); seg != nil {
		t.Error("cam_short has retain_days=1; its 2-day-old segment must be deleted even when global retention is unlimited")
	}
	if seg, _ := db.GetSegmentByPath(defaultOld); seg == nil {
		t.Error("cam_default falls back to the unlimited global retention; its old segment must be kept")
	}
}

// TestRunCleanup_RetainDaysZeroKeepsMotionActivity verifies motion activity is
// retained forever when retain_days <= 0.
func TestRunCleanup_RetainDaysZeroKeepsMotionActivity(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 0 // unlimited

	bucket := time.Now().Add(-100 * 24 * time.Hour)
	if err := db.SaveMotionActivity("cam1", bucket, 0.5); err != nil {
		t.Fatal(err)
	}

	rec.runCleanupLocked()

	got, err := db.GetMotionActivityInRange("cam1", bucket.Add(-time.Hour), bucket.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("retain_days=0 must keep motion activity, but the 100-day-old bucket was deleted")
	}
}

// TestRunCleanup_EventRetainZeroKeepsClips verifies event clip files are kept
// forever when event_retain_days <= 0.
func TestRunCleanup_EventRetainZeroKeepsClips(t *testing.T) {
	rec, _ := newTestRecorder(t)
	rec.config.RetainDays = 7  // segments cleanup enabled but irrelevant here
	rec.config.EventRetain = 0 // unlimited event media
	rec.eventConfig.RetainDays = 0

	clipDir := filepath.Join(rec.config.Path, "cam1", "clips", "2024-01-01")
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clipPath := filepath.Join(clipDir, "event.mp4")
	if err := os.WriteFile(clipPath, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	if err := os.Chtimes(clipPath, old, old); err != nil {
		t.Fatal(err)
	}

	rec.runCleanupLocked()

	if _, err := os.Stat(clipPath); err != nil {
		t.Error("event_retain_days=0 must keep clip files, but the old clip was deleted")
	}
}

// TestRunCleanup_EventMetadataRetainZeroKeepsEvents verifies event rows and
// linked faces are kept forever when the event retention is <= 0.
func TestRunCleanup_EventMetadataRetainZeroKeepsEvents(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 7
	rec.eventConfig.RetainDays = 0 // unlimited event metadata

	old := time.Now().Add(-100 * 24 * time.Hour)
	ev := camera.Event{
		ID:         "old-event",
		CameraName: "cam1",
		Label:      "person",
		Score:      0.9,
		Box:        [4]int{1, 2, 3, 4},
		Timestamp:  old,
	}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveFace(storage.Face{
		EventID:   ev.ID,
		Embedding: make([]byte, 128),
		Timestamp: old,
	}); err != nil {
		t.Fatal(err)
	}

	rec.runCleanupLocked()

	count, err := db.CountEvents("")
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("event_retain (metadata) <= 0 must keep events, but the old event was deleted")
	}
}

// TestRunCleanup_EventMetadataRetainPositiveDeletesOldEvents guards against the
// unlimited fix over-correcting: a positive retention must still delete events
// older than the window.
func TestRunCleanup_EventMetadataRetainPositiveDeletesOldEvents(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 7
	rec.eventConfig.RetainDays = 30

	old := time.Now().Add(-100 * 24 * time.Hour)
	if err := db.SaveEvent(camera.Event{
		ID:         "stale-event",
		CameraName: "cam1",
		Label:      "person",
		Score:      0.9,
		Box:        [4]int{1, 2, 3, 4},
		Timestamp:  old,
	}); err != nil {
		t.Fatal(err)
	}

	rec.runCleanupLocked()

	count, err := db.CountEvents("")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("event_retain=30 must delete a 100-day-old event, but %d remain", count)
	}
}

// TestRunCleanup_RetainDaysPositiveDeletesOldMotionActivity guards the motion
// activity path: a positive retention must still delete old buckets.
func TestRunCleanup_RetainDaysPositiveDeletesOldMotionActivity(t *testing.T) {
	rec, db := newTestRecorder(t)
	rec.config.RetainDays = 7

	bucket := time.Now().Add(-100 * 24 * time.Hour)
	if err := db.SaveMotionActivity("cam1", bucket, 0.5); err != nil {
		t.Fatal(err)
	}

	rec.runCleanupLocked()

	got, err := db.GetMotionActivityInRange("cam1", bucket.Add(-time.Hour), bucket.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("retain_days=7 must delete a 100-day-old motion bucket, but %d remain", len(got))
	}
}
