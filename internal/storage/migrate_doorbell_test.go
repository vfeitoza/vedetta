package storage

import (
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func TestMigration_DoorbellColumns(t *testing.T) {
	db := newTestDB(t)
	ev := camera.Event{ID: "cam-1", CameraName: "cam", Label: "person", Score: 0.9, Timestamp: time.Now()}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	got, err := db.GetEventByID("cam-1")
	if err != nil {
		t.Fatalf("GetEventByID: %v", err)
	}
	if got.Kind != "object" {
		t.Errorf("Kind = %q, want object (default)", got.Kind)
	}
}

func TestSaveAndScan_DoorbellEvent(t *testing.T) {
	db := newTestDB(t)
	ev := camera.Event{ID: "cam-doorbell-1", CameraName: "front_door", Label: "doorbell", Kind: camera.EventKindDoorbell, Score: 1.0, Timestamp: time.Now()}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	got, err := db.GetEventByID("cam-doorbell-1")
	if err != nil {
		t.Fatalf("GetEventByID: %v", err)
	}
	if got.Kind != camera.EventKindDoorbell {
		t.Errorf("Kind = %q, want doorbell", got.Kind)
	}
	if !got.AnsweredAt.IsZero() {
		t.Error("new ring should be unanswered")
	}
}

func TestUpdateEventAnswered(t *testing.T) {
	db := newTestDB(t)
	ev := camera.Event{ID: "r1", CameraName: "front_door", Label: "doorbell", Kind: camera.EventKindDoorbell, Timestamp: time.Now()}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateEventAnswered("r1", time.Now(), "claude"); err != nil {
		t.Fatalf("UpdateEventAnswered: %v", err)
	}
	got, _ := db.GetEventByID("r1")
	if got.AnsweredAt.IsZero() {
		t.Error("AnsweredAt not persisted")
	}
	if got.AnsweredBy != "claude" {
		t.Errorf("AnsweredBy = %q, want claude", got.AnsweredBy)
	}
}

func TestQueryEventsFiltered_Kind(t *testing.T) {
	db := newTestDB(t)
	_ = db.SaveEvent(camera.Event{ID: "o1", CameraName: "c", Label: "person", Kind: camera.EventKindObject, Timestamp: time.Now()})
	_ = db.SaveEvent(camera.Event{ID: "d1", CameraName: "c", Label: "doorbell", Kind: camera.EventKindDoorbell, Timestamp: time.Now()})
	got, err := db.QueryEventsFiltered(EventFilters{Kind: camera.EventKindDoorbell}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "d1" {
		t.Errorf("kind filter returned %d rows, want 1 (d1)", len(got))
	}
}
