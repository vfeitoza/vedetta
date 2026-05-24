package main

import (
	"context"
	"errors"
	"image"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/rvben/vedetta/internal/camera"
)

// --- test doubles (shared with tracing_event_test.go via package main) ---

type stubSnapshotSaver struct {
	called   bool
	gotEvent camera.Event
	resolved string
	err      error
}

func (s *stubSnapshotSaver) SaveEventSnapshot(event camera.Event, img *image.RGBA, primaryPath string) (string, error) {
	s.called = true
	s.gotEvent = event
	if s.err != nil {
		return "", s.err
	}
	return s.resolved, nil
}

type publishedEvent struct {
	ev      camera.Event
	objects []string
}

type fakeEventPublisher struct {
	events    []publishedEvent
	snapshots int
}

func (f *fakeEventPublisher) PublishEvent(event camera.Event, matchedObjects []string) error {
	f.events = append(f.events, publishedEvent{ev: event, objects: matchedObjects})
	return nil
}

func (f *fakeEventPublisher) PublishSnapshot(cameraName, label string, jpegData []byte) {
	f.snapshots++
}

type fakeEnqueuer struct {
	enqueued []camera.Event
}

func (f *fakeEnqueuer) Enqueue(ev camera.Event) { f.enqueued = append(f.enqueued, ev) }

func smallImage() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 2, 2)) }

// --- tests ---

func TestEmitEventArtifacts_Success(t *testing.T) {
	tracer, sr := newTestTracer()
	saver := &stubSnapshotSaver{resolved: "resolved/snap.jpg"}
	pub := &fakeEventPublisher{}
	enq := &fakeEnqueuer{}
	ev := camera.Event{
		ID:            "cam1-t1-1",
		CameraName:    "cam1",
		Label:         "person",
		SnapshotImage: smallImage(),
		SnapshotPath:  "primary/snap.jpg",
	}

	emitEventArtifacts(context.Background(), tracer, saver, pub, enq, 85, ev)

	if !saver.called {
		t.Fatal("saver not called")
	}
	if saver.gotEvent.ID != "cam1-t1-1" {
		t.Errorf("saver received event ID %q, want cam1-t1-1", saver.gotEvent.ID)
	}
	if spanByName(sr.Ended(), "snapshot.save") == nil {
		t.Error("snapshot.save span not recorded")
	}
	if spanByName(sr.Ended(), "mqtt.publish") == nil {
		t.Error("mqtt.publish span not recorded")
	}
	if len(pub.events) != 1 {
		t.Fatalf("PublishEvent called %d times, want 1", len(pub.events))
	}
	if got := pub.events[0].ev.SnapshotPath; got != "resolved/snap.jpg" {
		t.Errorf("published SnapshotPath = %q, want resolved/snap.jpg", got)
	}
	if !pub.events[0].ev.SnapshotAvailable {
		t.Error("published SnapshotAvailable = false, want true")
	}
	if pub.snapshots != 1 {
		t.Errorf("PublishSnapshot called %d times, want 1", pub.snapshots)
	}
	if len(enq.enqueued) != 1 {
		t.Fatalf("Enqueue called %d times, want 1", len(enq.enqueued))
	}
	if !enq.enqueued[0].SnapshotAvailable || enq.enqueued[0].SnapshotPath != "resolved/snap.jpg" {
		t.Errorf("enqueued event lost resolved snapshot: avail=%v path=%q",
			enq.enqueued[0].SnapshotAvailable, enq.enqueued[0].SnapshotPath)
	}
}

func TestEmitEventArtifacts_SnapshotError(t *testing.T) {
	tracer, sr := newTestTracer()
	saver := &stubSnapshotSaver{err: errors.New("disk full")}
	pub := &fakeEventPublisher{}
	enq := &fakeEnqueuer{}
	ev := camera.Event{
		ID:            "cam1-t1-2",
		CameraName:    "cam1",
		Label:         "person",
		SnapshotImage: smallImage(),
		SnapshotPath:  "primary/snap.jpg",
	}

	emitEventArtifacts(context.Background(), tracer, saver, pub, enq, 85, ev)

	span := spanByName(sr.Ended(), "snapshot.save")
	if span == nil {
		t.Fatal("snapshot.save span not recorded")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("snapshot.save status = %v, want Error", span.Status().Code)
	}
	if len(pub.events) != 1 {
		t.Errorf("PublishEvent called %d times, want 1 (publish proceeds after snapshot error)", len(pub.events))
	}
	if pub.events[0].ev.SnapshotAvailable {
		t.Error("published SnapshotAvailable = true, want false after snapshot error")
	}
	if len(enq.enqueued) != 1 {
		t.Fatalf("Enqueue called %d times, want 1", len(enq.enqueued))
	}
	if enq.enqueued[0].SnapshotAvailable {
		t.Error("enqueued SnapshotAvailable = true, want false after snapshot error")
	}
}

func TestEmitEventArtifacts_NoMQTTClient(t *testing.T) {
	tracer, sr := newTestTracer()
	saver := &stubSnapshotSaver{resolved: "resolved/snap.jpg"}
	enq := &fakeEnqueuer{}
	ev := camera.Event{
		ID:            "cam1-t1-3",
		CameraName:    "cam1",
		SnapshotImage: smallImage(),
		SnapshotPath:  "primary/snap.jpg",
	}

	emitEventArtifacts(context.Background(), tracer, saver, nil, enq, 85, ev)

	if spanByName(sr.Ended(), "mqtt.publish") != nil {
		t.Error("mqtt.publish span recorded, want none with nil publisher")
	}
	if !saver.called {
		t.Error("saver not called")
	}
	if len(enq.enqueued) != 1 {
		t.Errorf("Enqueue called %d times, want 1", len(enq.enqueued))
	}
}

func TestEmitEventArtifacts_NilSnapshotImage(t *testing.T) {
	tracer, sr := newTestTracer()
	saver := &stubSnapshotSaver{resolved: "resolved/snap.jpg"}
	pub := &fakeEventPublisher{}
	enq := &fakeEnqueuer{}
	ev := camera.Event{ID: "cam1-t1-4", CameraName: "cam1"} // no SnapshotImage

	emitEventArtifacts(context.Background(), tracer, saver, pub, enq, 85, ev)

	if spanByName(sr.Ended(), "snapshot.save") != nil {
		t.Error("snapshot.save span recorded, want none without a snapshot image")
	}
	if saver.called {
		t.Error("saver called, want skipped without a snapshot image")
	}
	if len(pub.events) != 1 {
		t.Errorf("PublishEvent called %d times, want 1", len(pub.events))
	}
	if len(enq.enqueued) != 1 {
		t.Errorf("Enqueue called %d times, want 1", len(enq.enqueued))
	}
}
