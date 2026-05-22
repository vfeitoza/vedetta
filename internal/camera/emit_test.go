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

// confirmTrack must only mark a track as confirmed when the start event is
// actually enqueued. If the channel is full and the event is dropped, the track
// must stay unconfirmed so the next frame retries instead of leaving an orphan
// track whose ID downstream event-end/face messages would reference.

func TestConfirmTrackMarksConfirmedOnEnqueue(t *testing.T) {
	ch := make(chan Event, 1)
	c := &Camera{config: config.CameraConfig{Name: "cam"}, events: ch, confirmedTracks: map[int]string{}}

	c.confirmTrack(7, Event{ID: "e7", Label: "person"})

	if got := c.confirmedTracks[7]; got != "e7" {
		t.Fatalf("track 7 confirmed event id = %q, want e7", got)
	}
	if got := <-ch; got.ID != "e7" {
		t.Fatalf("emitted event id = %q, want e7", got.ID)
	}
}

func TestConfirmTrackDoesNotConfirmWhenDropped(t *testing.T) {
	ch := make(chan Event, 1)
	c := &Camera{config: config.CameraConfig{Name: "cam"}, events: ch, confirmedTracks: map[int]string{}}

	c.confirmTrack(1, Event{ID: "e1"}) // enqueues, fills the single-slot buffer, marks track 1
	c.confirmTrack(2, Event{ID: "e2"}) // channel full: must drop and leave track 2 unconfirmed

	if _, ok := c.confirmedTracks[1]; !ok {
		t.Fatal("track 1 should be confirmed after a successful enqueue")
	}
	if _, ok := c.confirmedTracks[2]; ok {
		t.Fatal("track 2 marked confirmed despite a dropped start event")
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
