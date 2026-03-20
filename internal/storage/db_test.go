package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/watchpost/internal/camera"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func makeEvent(id, cam, label string, score float32, ts time.Time) camera.Event {
	return camera.Event{
		ID:         id,
		CameraName: cam,
		Label:      label,
		Score:      score,
		Box:        [4]int{10, 20, 100, 200},
		Timestamp:  ts,
	}
}

func makeSegment(cam, path string, start, end time.Time, size int64) SegmentRecord {
	return SegmentRecord{
		Camera:    cam,
		Path:      path,
		StartTime: start,
		EndTime:   end,
		SizeBytes: size,
	}
}

// --- Database creation ---

func TestNew_CreatesDatabase(t *testing.T) {
	db := newTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestNew_SchemaHasTables(t *testing.T) {
	db := newTestDB(t)

	// Check events table exists by inserting and querying
	err := db.SaveEvent(makeEvent("schema-test", "cam1", "person", 0.9, time.Now()))
	if err != nil {
		t.Fatalf("events table missing or broken: %v", err)
	}

	// Check segments table exists
	err = db.SaveSegment(makeSegment("cam1", "/tmp/seg.mp4", time.Now(), time.Now().Add(time.Minute), 1024))
	if err != nil {
		t.Fatalf("segments table missing or broken: %v", err)
	}
}

// --- Event operations ---

func TestSaveEvent_GetEventByID_Roundtrip(t *testing.T) {
	db := newTestDB(t)
	ts := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	ev := makeEvent("ev1", "front_door", "person", 0.95, ts)
	ev.SnapshotPath = "/snapshots/ev1.jpg"
	ev.ClipPath = "/clips/ev1.mp4"

	if err := db.SaveEvent(ev); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	got, err := db.GetEventByID("ev1")
	if err != nil {
		t.Fatalf("GetEventByID: %v", err)
	}
	if got == nil {
		t.Fatal("expected event, got nil")
	}
	if got.ID != "ev1" {
		t.Errorf("ID = %q, want %q", got.ID, "ev1")
	}
	if got.CameraName != "front_door" {
		t.Errorf("CameraName = %q, want %q", got.CameraName, "front_door")
	}
	if got.Label != "person" {
		t.Errorf("Label = %q, want %q", got.Label, "person")
	}
	if got.Score != 0.95 {
		t.Errorf("Score = %v, want 0.95", got.Score)
	}
	if got.Box != [4]int{10, 20, 100, 200} {
		t.Errorf("Box = %v, want [10 20 100 200]", got.Box)
	}
	if got.SnapshotPath != "/snapshots/ev1.jpg" {
		t.Errorf("SnapshotPath = %q, want %q", got.SnapshotPath, "/snapshots/ev1.jpg")
	}
	if got.ClipPath != "/clips/ev1.mp4" {
		t.Errorf("ClipPath = %q, want %q", got.ClipPath, "/clips/ev1.mp4")
	}
}

func TestGetEventByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	got, err := db.GetEventByID("nonexistent")
	if err != nil {
		t.Fatalf("GetEventByID: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent ID, got %+v", got)
	}
}

func TestQueryEvents_NoFilters(t *testing.T) {
	db := newTestDB(t)
	ts := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i, label := range []string{"person", "car", "dog"} {
		ev := makeEvent("ev"+string(rune('a'+i)), "cam1", label, 0.9, ts.Add(time.Duration(i)*time.Minute))
		if err := db.SaveEvent(ev); err != nil {
			t.Fatalf("SaveEvent: %v", err)
		}
	}

	events, err := db.QueryEvents("", "", 0, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
}

func TestQueryEvents_FilterByCamera(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveEvent(makeEvent("e1", "cam1", "person", 0.9, ts))
	db.SaveEvent(makeEvent("e2", "cam2", "person", 0.8, ts))
	db.SaveEvent(makeEvent("e3", "cam1", "car", 0.7, ts))

	events, err := db.QueryEvents("cam1", "", 0, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.CameraName != "cam1" {
			t.Errorf("expected cam1, got %q", e.CameraName)
		}
	}
}

func TestQueryEvents_FilterByLabel(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveEvent(makeEvent("e1", "cam1", "person", 0.9, ts))
	db.SaveEvent(makeEvent("e2", "cam1", "car", 0.8, ts))
	db.SaveEvent(makeEvent("e3", "cam2", "person", 0.7, ts))

	events, err := db.QueryEvents("", "person", 0, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.Label != "person" {
			t.Errorf("expected label person, got %q", e.Label)
		}
	}
}

func TestQueryEvents_WithLimit(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	for i := range 5 {
		db.SaveEvent(makeEvent("e"+string(rune('0'+i)), "cam1", "person", 0.9, ts.Add(time.Duration(i)*time.Minute)))
	}

	events, err := db.QueryEvents("", "", 2, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
}

func TestQueryEvents_FilterByCameraAndLabel(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveEvent(makeEvent("e1", "cam1", "person", 0.9, ts))
	db.SaveEvent(makeEvent("e2", "cam1", "car", 0.8, ts))
	db.SaveEvent(makeEvent("e3", "cam2", "person", 0.7, ts))
	db.SaveEvent(makeEvent("e4", "cam2", "car", 0.6, ts))

	events, err := db.QueryEvents("cam1", "person", 0, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].ID != "e1" {
		t.Errorf("got ID %q, want e1", events[0].ID)
	}
}

func TestQueryEvents_OrderByTimestampDesc(t *testing.T) {
	db := newTestDB(t)
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	db.SaveEvent(makeEvent("e1", "cam1", "person", 0.9, t1))
	db.SaveEvent(makeEvent("e2", "cam1", "person", 0.9, t3))
	db.SaveEvent(makeEvent("e3", "cam1", "person", 0.9, t2))

	events, err := db.QueryEvents("", "", 0, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if events[0].ID != "e2" || events[1].ID != "e3" || events[2].ID != "e1" {
		t.Errorf("events not ordered by timestamp desc: %v, %v, %v", events[0].ID, events[1].ID, events[2].ID)
	}
}

func TestCountEventsToday(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour).Add(1 * time.Hour) // 01:00 today
	yesterday := today.Add(-25 * time.Hour)

	db.SaveEvent(makeEvent("today1", "cam1", "person", 0.9, today))
	db.SaveEvent(makeEvent("today2", "cam1", "car", 0.8, today.Add(time.Hour)))
	db.SaveEvent(makeEvent("yesterday1", "cam1", "person", 0.7, yesterday))

	count, err := db.CountEventsToday()
	if err != nil {
		t.Fatalf("CountEventsToday: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestUpdateEventClipPath(t *testing.T) {
	db := newTestDB(t)
	ev := makeEvent("clip-test", "cam1", "person", 0.9, time.Now().UTC())
	db.SaveEvent(ev)

	if err := db.UpdateEventClipPath("clip-test", "/clips/new.mp4"); err != nil {
		t.Fatalf("UpdateEventClipPath: %v", err)
	}

	got, _ := db.GetEventByID("clip-test")
	if got.ClipPath != "/clips/new.mp4" {
		t.Errorf("ClipPath = %q, want %q", got.ClipPath, "/clips/new.mp4")
	}
}

func TestUpdateEventSnapshotPath(t *testing.T) {
	db := newTestDB(t)
	ev := makeEvent("snap-test", "cam1", "person", 0.9, time.Now().UTC())
	db.SaveEvent(ev)

	if err := db.UpdateEventSnapshotPath("snap-test", "/snapshots/new.jpg"); err != nil {
		t.Fatalf("UpdateEventSnapshotPath: %v", err)
	}

	got, _ := db.GetEventByID("snap-test")
	if got.SnapshotPath != "/snapshots/new.jpg" {
		t.Errorf("SnapshotPath = %q, want %q", got.SnapshotPath, "/snapshots/new.jpg")
	}
}

// --- Segment operations ---

func TestGetSegmentByPath_Found(t *testing.T) {
	db := newTestDB(t)
	start := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	seg := makeSegment("cam1", "/recordings/cam1/seg001.mp4", start, start.Add(5*time.Minute), 5000)
	db.SaveSegment(seg)

	got, err := db.GetSegmentByPath("/recordings/cam1/seg001.mp4")
	if err != nil {
		t.Fatalf("GetSegmentByPath: %v", err)
	}
	if got == nil {
		t.Fatal("expected segment, got nil")
	}
	if got.Camera != "cam1" {
		t.Errorf("Camera = %q, want %q", got.Camera, "cam1")
	}
	if got.SizeBytes != 5000 {
		t.Errorf("SizeBytes = %d, want 5000", got.SizeBytes)
	}
}

func TestGetSegmentByPath_NotFound(t *testing.T) {
	db := newTestDB(t)
	got, err := db.GetSegmentByPath("/nonexistent.mp4")
	if err != nil {
		t.Fatalf("GetSegmentByPath: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing path, got %+v", got)
	}
}

func TestGetSegmentsForDate(t *testing.T) {
	db := newTestDB(t)
	march20 := time.Date(2026, 3, 20, 14, 0, 0, 0, time.UTC)
	march21 := time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)

	db.SaveSegment(makeSegment("cam1", "/seg1.mp4", march20, march20.Add(5*time.Minute), 1000))
	db.SaveSegment(makeSegment("cam1", "/seg2.mp4", march20.Add(time.Hour), march20.Add(time.Hour+5*time.Minute), 2000))
	db.SaveSegment(makeSegment("cam1", "/seg3.mp4", march21, march21.Add(5*time.Minute), 3000))

	segs, err := db.GetSegmentsForDate("cam1", march20)
	if err != nil {
		t.Fatalf("GetSegmentsForDate: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
}

func TestGetSegmentsForDate_DifferentCamera(t *testing.T) {
	db := newTestDB(t)
	ts := time.Date(2026, 3, 20, 14, 0, 0, 0, time.UTC)

	db.SaveSegment(makeSegment("cam1", "/seg1.mp4", ts, ts.Add(5*time.Minute), 1000))
	db.SaveSegment(makeSegment("cam2", "/seg2.mp4", ts, ts.Add(5*time.Minute), 2000))

	segs, err := db.GetSegmentsForDate("cam1", ts)
	if err != nil {
		t.Fatalf("GetSegmentsForDate: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1", len(segs))
	}
}

func TestTotalSegmentBytes(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveSegment(makeSegment("cam1", "/s1.mp4", ts, ts.Add(time.Minute), 1000))
	db.SaveSegment(makeSegment("cam1", "/s2.mp4", ts, ts.Add(time.Minute), 2000))
	db.SaveSegment(makeSegment("cam2", "/s3.mp4", ts, ts.Add(time.Minute), 3000))

	total, err := db.TotalSegmentBytes()
	if err != nil {
		t.Fatalf("TotalSegmentBytes: %v", err)
	}
	if total != 6000 {
		t.Errorf("total = %d, want 6000", total)
	}
}

func TestTotalSegmentBytes_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	total, err := db.TotalSegmentBytes()
	if err != nil {
		t.Fatalf("TotalSegmentBytes: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 for empty DB", total)
	}
}

func TestSegmentBytesByCamera(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveSegment(makeSegment("cam1", "/s1.mp4", ts, ts.Add(time.Minute), 1000))
	db.SaveSegment(makeSegment("cam1", "/s2.mp4", ts, ts.Add(time.Minute), 2000))
	db.SaveSegment(makeSegment("cam2", "/s3.mp4", ts, ts.Add(time.Minute), 5000))

	result, err := db.SegmentBytesByCamera()
	if err != nil {
		t.Fatalf("SegmentBytesByCamera: %v", err)
	}
	if result["cam1"] != 3000 {
		t.Errorf("cam1 = %d, want 3000", result["cam1"])
	}
	if result["cam2"] != 5000 {
		t.Errorf("cam2 = %d, want 5000", result["cam2"])
	}
}

func TestCountSegments(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveSegment(makeSegment("cam1", "/s1.mp4", ts, ts.Add(time.Minute), 100))
	db.SaveSegment(makeSegment("cam1", "/s2.mp4", ts, ts.Add(time.Minute), 200))
	db.SaveSegment(makeSegment("cam2", "/s3.mp4", ts, ts.Add(time.Minute), 300))

	count, err := db.CountSegments()
	if err != nil {
		t.Fatalf("CountSegments: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestCountSegments_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	count, err := db.CountSegments()
	if err != nil {
		t.Fatalf("CountSegments: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestDeleteSegment(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()
	db.SaveSegment(makeSegment("cam1", "/del.mp4", ts, ts.Add(time.Minute), 100))

	if err := db.DeleteSegment("/del.mp4"); err != nil {
		t.Fatalf("DeleteSegment: %v", err)
	}

	got, _ := db.GetSegmentByPath("/del.mp4")
	if got != nil {
		t.Error("segment still exists after delete")
	}
}

func TestGetOldestSegments(t *testing.T) {
	db := newTestDB(t)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	db.SaveSegment(makeSegment("cam1", "/s3.mp4", t3, t3.Add(time.Minute), 100))
	db.SaveSegment(makeSegment("cam1", "/s1.mp4", t1, t1.Add(time.Minute), 100))
	db.SaveSegment(makeSegment("cam1", "/s2.mp4", t2, t2.Add(time.Minute), 100))

	segs, err := db.GetOldestSegments(2)
	if err != nil {
		t.Fatalf("GetOldestSegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	if segs[0].Path != "/s1.mp4" {
		t.Errorf("oldest segment path = %q, want /s1.mp4", segs[0].Path)
	}
	if segs[1].Path != "/s2.mp4" {
		t.Errorf("second oldest path = %q, want /s2.mp4", segs[1].Path)
	}
}

func TestQuerySegments_TimeRange(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	// Segment from 12:00-12:05
	db.SaveSegment(makeSegment("cam1", "/s1.mp4", base, base.Add(5*time.Minute), 100))
	// Segment from 12:10-12:15
	db.SaveSegment(makeSegment("cam1", "/s2.mp4", base.Add(10*time.Minute), base.Add(15*time.Minute), 100))
	// Segment from 12:20-12:25
	db.SaveSegment(makeSegment("cam1", "/s3.mp4", base.Add(20*time.Minute), base.Add(25*time.Minute), 100))

	// Query range 12:03-12:12 should overlap s1 (ends at 12:05 > 12:03) and s2 (starts at 12:10 < 12:12)
	segs, err := db.QuerySegments("cam1", base.Add(3*time.Minute), base.Add(12*time.Minute))
	if err != nil {
		t.Fatalf("QuerySegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
}

func TestGetAllSegments(t *testing.T) {
	db := newTestDB(t)
	ts := time.Now().UTC()

	db.SaveSegment(makeSegment("cam1", "/a.mp4", ts, ts.Add(time.Minute), 100))
	db.SaveSegment(makeSegment("cam1", "/b.mp4", ts.Add(time.Minute), ts.Add(2*time.Minute), 200))
	db.SaveSegment(makeSegment("cam2", "/c.mp4", ts, ts.Add(time.Minute), 300))

	segs, err := db.GetAllSegments("cam1")
	if err != nil {
		t.Fatalf("GetAllSegments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments for cam1, want 2", len(segs))
	}
}
