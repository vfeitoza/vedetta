package camera

import "testing"

func TestEventKindConstants(t *testing.T) {
	if EventKindObject != "object" {
		t.Errorf("EventKindObject = %q, want %q", EventKindObject, "object")
	}
	if EventKindDoorbell != "doorbell" {
		t.Errorf("EventKindDoorbell = %q, want %q", EventKindDoorbell, "doorbell")
	}
}

func TestEventKindField(t *testing.T) {
	ev := Event{Kind: EventKindDoorbell, AnsweredBy: "claude"}
	if ev.Kind != "doorbell" {
		t.Errorf("Kind = %q, want doorbell", ev.Kind)
	}
	if ev.AnsweredBy != "claude" {
		t.Errorf("AnsweredBy = %q, want claude", ev.AnsweredBy)
	}
	if !ev.AnsweredAt.IsZero() {
		t.Error("AnsweredAt should default to zero")
	}
}
