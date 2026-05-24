package main

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

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
	mu       sync.Mutex
	enqueued []camera.Event
}

func (f *fakeEnqueuer) Enqueue(ev camera.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, ev)
}

// count returns the number of enqueued events under lock, safe to call while the
// emit goroutine may still be appending.
func (f *fakeEnqueuer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.enqueued)
}

// at returns the i-th enqueued event under lock.
func (f *fakeEnqueuer) at(i int) camera.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enqueued[i]
}

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
	if enq.count() != 1 {
		t.Fatalf("Enqueue called %d times, want 1", enq.count())
	}
	got := enq.at(0)
	if !got.SnapshotAvailable || got.SnapshotPath != "resolved/snap.jpg" {
		t.Errorf("enqueued event lost resolved snapshot: avail=%v path=%q",
			got.SnapshotAvailable, got.SnapshotPath)
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
	if enq.count() != 1 {
		t.Fatalf("Enqueue called %d times, want 1", enq.count())
	}
	if enq.at(0).SnapshotAvailable {
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
	if enq.count() != 1 {
		t.Errorf("Enqueue called %d times, want 1", enq.count())
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
	if enq.count() != 1 {
		t.Errorf("Enqueue called %d times, want 1", enq.count())
	}
}

func TestWaitForEmit_NilReturnsImmediately(t *testing.T) {
	start := time.Now()
	waitForEmit(context.Background(), nil, time.Second)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("nil channel waited %v, want immediate", elapsed)
	}
}

func TestWaitForEmit_ClosedReturnsImmediately(t *testing.T) {
	done := make(chan struct{})
	close(done)
	start := time.Now()
	waitForEmit(context.Background(), done, time.Second)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("closed channel waited %v, want immediate", elapsed)
	}
}

func TestWaitForEmit_TimesOut(t *testing.T) {
	done := make(chan struct{}) // never closed
	start := time.Now()
	waitForEmit(context.Background(), done, 30*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("waited %v, want at least ~30ms timeout", elapsed)
	}
}

func TestWaitForEmit_ContextCancel(t *testing.T) {
	done := make(chan struct{}) // never closed
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	waitForEmit(ctx, done, time.Second)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("cancelled ctx waited %v, want immediate", elapsed)
	}
}
