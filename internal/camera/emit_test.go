package camera

import (
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/config"
)

// emitEvent and emitEventEnd must never block the detector goroutine: when the
// downstream channel is full they drop and warn, matching the presence/face/
// detection channels.

func TestEmitEventDeliversWhenNotFull(t *testing.T) {
	ch := make(chan Event, 1)
	c := &Camera{config: config.CameraConfig{Name: "cam"}, events: ch}

	c.emitEvent(Event{Label: "person"})

	got := <-ch
	if got.Label != "person" {
		t.Fatalf("want person, got %q", got.Label)
	}
}

func TestEmitEventDropsWhenFull(t *testing.T) {
	ch := make(chan Event, 1)
	c := &Camera{config: config.CameraConfig{Name: "cam"}, events: ch}

	c.emitEvent(Event{Label: "person"}) // fills the single-slot buffer

	done := make(chan struct{})
	go func() {
		c.emitEvent(Event{Label: "car"}) // must not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitEvent blocked when channel full")
	}

	got := <-ch
	if got.Label != "person" {
		t.Fatalf("want buffered person, got %q", got.Label)
	}
	select {
	case extra := <-ch:
		t.Fatalf("expected the second event to be dropped, got %q", extra.Label)
	default:
	}
}

func TestEmitEventEndDropsWhenFull(t *testing.T) {
	ch := make(chan EventEnd, 1)
	c := &Camera{config: config.CameraConfig{Name: "cam"}, eventEnds: ch}

	c.emitEventEnd(EventEnd{EventID: "a"}) // fills buffer

	done := make(chan struct{})
	go func() {
		c.emitEventEnd(EventEnd{EventID: "b"}) // must not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitEventEnd blocked when channel full")
	}

	got := <-ch
	if got.EventID != "a" {
		t.Fatalf("want buffered event end a, got %q", got.EventID)
	}
}
