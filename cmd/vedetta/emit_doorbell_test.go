package main

import (
	"context"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// fakeDoorbellPub satisfies eventPublisher (including PublishDoorbell) for
// doorbell-specific emit tests. The existing fakeEventPublisher in
// emit_event_test.go already has PublishDoorbell added; this is a standalone
// fake used only in this file so the assertions read cleanly.
type fakeDoorbellPub struct {
	doorbellCalls int
	lastPerson    string
	lastCam       string
}

func (f *fakeDoorbellPub) PublishEvent(ev camera.Event, matched []string) error { return nil }
func (f *fakeDoorbellPub) PublishSnapshot(cam, label string, jpeg []byte)       {}
func (f *fakeDoorbellPub) PublishDoorbell(cam, person string, jpeg []byte) {
	f.doorbellCalls++
	f.lastPerson = person
	f.lastCam = cam
}

func TestEmitEventArtifacts_PublishesDoorbell(t *testing.T) {
	tracer, _ := newTestTracer()
	pub := &fakeDoorbellPub{}
	ev := camera.Event{
		ID:         "r1",
		CameraName: "front_door",
		Label:      "doorbell",
		Kind:       camera.EventKindDoorbell,
		SubLabel:   "Alice",
		Timestamp:  time.Now(),
	}
	emitEventArtifacts(context.Background(), tracer, &stubSnapshotSaver{}, pub, nil, 80, ev)
	if pub.doorbellCalls != 1 {
		t.Errorf("PublishDoorbell calls = %d, want 1", pub.doorbellCalls)
	}
	if pub.lastPerson != "Alice" {
		t.Errorf("person = %q, want Alice", pub.lastPerson)
	}
	if pub.lastCam != "front_door" {
		t.Errorf("camera = %q, want front_door", pub.lastCam)
	}
}

func TestEmitEventArtifacts_ObjectEventNoDoorbell(t *testing.T) {
	tracer, _ := newTestTracer()
	pub := &fakeDoorbellPub{}
	ev := camera.Event{
		ID:         "o1",
		CameraName: "c",
		Label:      "person",
		Kind:       camera.EventKindObject,
		Timestamp:  time.Now(),
	}
	emitEventArtifacts(context.Background(), tracer, &stubSnapshotSaver{}, pub, nil, 80, ev)
	if pub.doorbellCalls != 0 {
		t.Errorf("object event must not publish doorbell, got %d call(s)", pub.doorbellCalls)
	}
}
