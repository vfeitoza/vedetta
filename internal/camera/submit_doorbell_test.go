package camera

import (
	"context"
	"image"
	"testing"
)

func TestSubmitDoorbellPress(t *testing.T) {
	events := make(chan Event, 4)

	m := &Manager{
		cameras:     make(map[string]*Camera),
		cancelFuncs: make(map[string]context.CancelFunc),
		order:       []string{},
		events:      events,
	}

	cam := NewTestCamera("front_door")
	frame := image.NewRGBA(image.Rect(0, 0, 640, 480))
	cam.SetTestFrame(frame)
	m.RegisterForTest(cam)

	id, ok := m.SubmitDoorbellPress("front_door")
	if !ok {
		t.Fatal("SubmitDoorbellPress: got ok=false for a camera with a frame, want true")
	}
	if id == "" {
		t.Fatal("SubmitDoorbellPress: got empty event ID, want non-empty")
	}

	var ev Event
	select {
	case ev = <-events:
	default:
		t.Fatal("SubmitDoorbellPress: no event emitted to events channel")
	}

	if ev.Kind != EventKindDoorbell {
		t.Errorf("event.Kind = %q, want %q", ev.Kind, EventKindDoorbell)
	}
	if ev.Label != "doorbell" {
		t.Errorf("event.Label = %q, want %q", ev.Label, "doorbell")
	}
	if ev.Category != CategoryAlert {
		t.Errorf("event.Category = %q, want %q", ev.Category, CategoryAlert)
	}
	if ev.SnapshotImage == nil {
		t.Error("event.SnapshotImage is nil, want non-nil RGBA frame")
	}
	if ev.ID != id {
		t.Errorf("event.ID = %q, want %q (returned id)", ev.ID, id)
	}
	if ev.CameraName != "front_door" {
		t.Errorf("event.CameraName = %q, want %q", ev.CameraName, "front_door")
	}
}

func TestSubmitDoorbellPressUnknownCamera(t *testing.T) {
	events := make(chan Event, 4)

	m := &Manager{
		cameras: make(map[string]*Camera),
		events:  events,
	}

	id, ok := m.SubmitDoorbellPress("does_not_exist")
	if ok {
		t.Fatalf("SubmitDoorbellPress: got ok=true for unknown camera, want false")
	}
	if id != "" {
		t.Fatalf("SubmitDoorbellPress: got id=%q for unknown camera, want empty", id)
	}
}

func TestSubmitDoorbellPressNoFrame(t *testing.T) {
	events := make(chan Event, 4)

	m := &Manager{
		cameras: make(map[string]*Camera),
		events:  events,
	}

	cam := NewTestCamera("back_door")
	// Do NOT set a frame - LastSnapshot() returns nil.
	m.RegisterForTest(cam)

	id, ok := m.SubmitDoorbellPress("back_door")
	if ok {
		t.Fatalf("SubmitDoorbellPress: got ok=true for camera with no frame, want false")
	}
	if id != "" {
		t.Fatalf("SubmitDoorbellPress: got id=%q for camera with no frame, want empty", id)
	}
}
