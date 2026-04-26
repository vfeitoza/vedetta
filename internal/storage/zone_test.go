package storage

import (
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func makeZone(cam, name string, x1, y1, x2, y2 float64) camera.Zone {
	return camera.Zone{
		Camera:  cam,
		Name:    name,
		X1:      x1,
		Y1:      y1,
		X2:      x2,
		Y2:      y2,
		Labels:  []string{"person", "car"},
		Enabled: true,
	}
}

func TestSaveZone_ListZones(t *testing.T) {
	db := newTestDB(t)

	z1 := makeZone("cam1", "driveway", 0.0, 0.2, 0.5, 0.8)
	z2 := makeZone("cam1", "doorbell", 0.3, 0.4, 0.7, 0.9)
	z3 := makeZone("cam2", "backyard", 0.0, 0.0, 1.0, 1.0)

	for _, z := range []camera.Zone{z1, z2, z3} {
		if err := db.SaveZone(z); err != nil {
			t.Fatalf("SaveZone(%s/%s): %v", z.Camera, z.Name, err)
		}
	}

	zones, err := db.ListZones("cam1")
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("expected 2 zones for cam1, got %d", len(zones))
	}

	// Should be ordered by name
	if zones[0].Name != "doorbell" {
		t.Errorf("zones[0].Name = %q, want doorbell", zones[0].Name)
	}
	if zones[1].Name != "driveway" {
		t.Errorf("zones[1].Name = %q, want driveway", zones[1].Name)
	}
}

func TestSaveZone_Upsert(t *testing.T) {
	db := newTestDB(t)

	z := makeZone("cam1", "driveway", 0.0, 0.0, 0.5, 0.5)
	if err := db.SaveZone(z); err != nil {
		t.Fatalf("SaveZone: %v", err)
	}

	// Update the same zone
	z.X2 = 0.8
	z.Labels = []string{"truck"}
	z.TrackPresence = true
	if err := db.SaveZone(z); err != nil {
		t.Fatalf("SaveZone (upsert): %v", err)
	}

	zones, err := db.ListZones("cam1")
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 1 {
		t.Fatalf("expected 1 zone after upsert, got %d", len(zones))
	}
	if zones[0].X2 != 0.8 {
		t.Errorf("X2 = %f, want 0.8", zones[0].X2)
	}
	if !zones[0].TrackPresence {
		t.Error("expected TrackPresence=true")
	}
	if len(zones[0].Labels) != 1 || zones[0].Labels[0] != "truck" {
		t.Errorf("Labels = %v, want [truck]", zones[0].Labels)
	}
}

func TestGetZone(t *testing.T) {
	db := newTestDB(t)

	z := makeZone("cam1", "driveway", 0.0, 0.2, 0.5, 0.8)
	z.FaceRecognition = true
	if err := db.SaveZone(z); err != nil {
		t.Fatalf("SaveZone: %v", err)
	}

	got, err := db.GetZone("cam1", "driveway")
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if got == nil {
		t.Fatal("expected zone, got nil")
	}
	if got.Name != "driveway" {
		t.Errorf("Name = %q, want driveway", got.Name)
	}
	if got.Camera != "cam1" {
		t.Errorf("Camera = %q, want cam1", got.Camera)
	}
	if !got.FaceRecognition {
		t.Error("expected FaceRecognition=true")
	}
	if got.X1 != 0.0 || got.Y1 != 0.2 || got.X2 != 0.5 || got.Y2 != 0.8 {
		t.Errorf("coordinates = (%f,%f,%f,%f), want (0.0,0.2,0.5,0.8)", got.X1, got.Y1, got.X2, got.Y2)
	}
}

func TestGetZone_NotFound(t *testing.T) {
	db := newTestDB(t)

	got, err := db.GetZone("cam1", "nonexistent")
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestDeleteZone(t *testing.T) {
	db := newTestDB(t)

	z := makeZone("cam1", "driveway", 0.0, 0.0, 0.5, 0.5)
	if err := db.SaveZone(z); err != nil {
		t.Fatalf("SaveZone: %v", err)
	}

	if err := db.DeleteZone("cam1", "driveway"); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}

	got, err := db.GetZone("cam1", "driveway")
	if err != nil {
		t.Fatalf("GetZone after delete: %v", err)
	}
	if got != nil {
		t.Error("zone still exists after delete")
	}
}

func TestDeleteZone_NonExistent(t *testing.T) {
	db := newTestDB(t)

	// Should not error
	if err := db.DeleteZone("cam1", "nonexistent"); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}
}

func TestListZones_Empty(t *testing.T) {
	db := newTestDB(t)

	zones, err := db.ListZones("cam1")
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 0 {
		t.Fatalf("expected 0 zones, got %d", len(zones))
	}
}

func TestListZones_DifferentCameras(t *testing.T) {
	db := newTestDB(t)

	if err := db.SaveZone(makeZone("cam1", "z1", 0, 0, 0.5, 0.5)); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveZone(makeZone("cam2", "z2", 0, 0, 0.5, 0.5)); err != nil {
		t.Fatal(err)
	}

	zones, err := db.ListZones("cam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 1 {
		t.Fatalf("expected 1 zone for cam1, got %d", len(zones))
	}
	if zones[0].Name != "z1" {
		t.Errorf("expected z1, got %q", zones[0].Name)
	}
}

func TestZonePresence_CRUD(t *testing.T) {
	db := newTestDB(t)

	z := makeZone("cam1", "driveway", 0.0, 0.0, 0.5, 0.5)
	z.TrackPresence = true
	if err := db.SaveZone(z); err != nil {
		t.Fatal(err)
	}

	zone, err := db.GetZone("cam1", "driveway")
	if err != nil {
		t.Fatal(err)
	}

	// Update presence
	if err := db.UpdateZonePresence(zone.ID, "car", true); err != nil {
		t.Fatalf("UpdateZonePresence: %v", err)
	}

	pres, err := db.GetZonePresence(zone.ID)
	if err != nil {
		t.Fatalf("GetZonePresence: %v", err)
	}
	if len(pres) != 1 {
		t.Fatalf("expected 1 presence record, got %d", len(pres))
	}
	if !pres[0].Present {
		t.Error("expected present=true")
	}
	if pres[0].Label != "car" {
		t.Errorf("label = %q, want car", pres[0].Label)
	}

	// Update to not present
	if err := db.UpdateZonePresence(zone.ID, "car", false); err != nil {
		t.Fatalf("UpdateZonePresence (false): %v", err)
	}

	pres, err = db.GetZonePresence(zone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pres) != 1 {
		t.Fatalf("expected 1 presence record, got %d", len(pres))
	}
	if pres[0].Present {
		t.Error("expected present=false")
	}
}

func TestUpdateEventZone(t *testing.T) {
	db := newTestDB(t)

	ev := makeEvent("ev-zone", "cam1", "person", 0.9, mustParseTime("2026-03-20T12:00:00Z"))
	mustSaveEvent(t, db, ev)

	if err := db.UpdateEventZone("ev-zone", "doorbell"); err != nil {
		t.Fatalf("UpdateEventZone: %v", err)
	}

	got, err := db.GetEventByID("ev-zone")
	if err != nil {
		t.Fatal(err)
	}
	if got.ZoneName != "doorbell" {
		t.Errorf("ZoneName = %q, want doorbell", got.ZoneName)
	}
}

func TestQueryEventsFiltered_ByZone(t *testing.T) {
	db := newTestDB(t)

	ev1 := makeEvent("e1", "cam1", "person", 0.9, mustParseTime("2026-03-20T12:00:00Z"))
	ev1.ZoneName = "doorbell"
	mustSaveEvent(t, db, ev1)

	ev2 := makeEvent("e2", "cam1", "car", 0.8, mustParseTime("2026-03-20T12:01:00Z"))
	ev2.ZoneName = "driveway"
	mustSaveEvent(t, db, ev2)

	ev3 := makeEvent("e3", "cam1", "person", 0.7, mustParseTime("2026-03-20T12:02:00Z"))
	mustSaveEvent(t, db, ev3)

	events, err := db.QueryEventsFiltered(EventFilters{Zone: "doorbell"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event with zone 'doorbell', got %d", len(events))
	}
	if events[0].ID != "e1" {
		t.Errorf("expected e1, got %q", events[0].ID)
	}
}

func TestSaveZone_EmptyLabels(t *testing.T) {
	db := newTestDB(t)

	z := camera.Zone{
		Camera:  "cam1",
		Name:    "catch_all",
		X1:      0.0,
		Y1:      0.0,
		X2:      1.0,
		Y2:      1.0,
		Labels:  nil,
		Enabled: true,
	}
	if err := db.SaveZone(z); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetZone("cam1", "catch_all")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected zone, got nil")
	}
	// nil labels should come back as nil/empty after JSON roundtrip
	if len(got.Labels) != 0 {
		t.Errorf("expected empty labels, got %v", got.Labels)
	}
}
