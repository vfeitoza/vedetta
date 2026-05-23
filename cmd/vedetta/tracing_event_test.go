package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// newTestTracer returns a tracer backed by an in-memory recorder so tests can
// assert on the spans runEventLoop produces.
func newTestTracer() (trace.Tracer, *tracetest.SpanRecorder) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	return tp.Tracer("test"), sr
}

// spanByName returns the first recorded span with the given name, or nil.
func spanByName(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// waitForSpan polls the recorder until a span with the given name is ended or
// the deadline passes.
func waitForSpan(t *testing.T, sr *tracetest.SpanRecorder, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if spanByName(sr.Ended(), name) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("span %q not recorded within deadline", name)
}

// testSubsystems builds a minimal subsystems with only the channels populated.
// MQTT client, object embedder, recorder, notifier are left nil: the event used
// in the test carries no snapshot image, so snapshot.save and mqtt.publish are
// skipped, and the clip goroutine returns on ctx cancel before touching the
// (nil) recorder.
func testSubsystems() *subsystems {
	sub := &subsystems{}
	sub.events = make(chan camera.Event, 4)
	sub.eventEnds = make(chan camera.EventEnd, 4)
	sub.presenceEvents = make(chan camera.PresenceEvent, 4)
	sub.faceEvents = make(chan camera.FaceEvent, 4)
	sub.motionActivity = make(chan camera.MotionActivity, 4)
	sub.detections = make(chan camera.DetectionFrame, 4)
	return sub
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Recording.Continuous = true // skip temp-recording branch (no recorder)
	cfg.Recording.MaxEventDuration = time.Hour
	cfg.Events.CooldownSeconds = 0
	return cfg
}

func TestRunEventLoopTracingRoot(t *testing.T) {
	tracer, sr := newTestTracer()
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sub := testSubsystems()
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runEventLoop(ctx, cfg, db, sub, nil, tracer)

	sub.events <- camera.Event{
		ID:         "cam1-t7-123",
		CameraName: "cam1",
		Label:      "person",
		Score:      0.91,
		TrackID:    7,
		ZoneName:   "driveway",
		Timestamp:  time.Now(),
	}

	waitForSpan(t, sr, "event")

	spans := sr.Ended()
	root := spanByName(spans, "event")
	dbSpan := spanByName(spans, "db.save_event")
	if root == nil || dbSpan == nil {
		t.Fatalf("missing spans: event=%v db.save_event=%v", root != nil, dbSpan != nil)
	}
	if dbSpan.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Errorf("db.save_event parent = %v, want root %v", dbSpan.Parent().SpanID(), root.SpanContext().SpanID())
	}
	if dbSpan.SpanContext().TraceID() != root.SpanContext().TraceID() {
		t.Errorf("db.save_event trace id != root trace id")
	}

	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range root.Attributes() {
		attrs[kv.Key] = kv.Value
	}
	if got := attrs["vedetta.camera"].AsString(); got != "cam1" {
		t.Errorf("vedetta.camera = %q, want cam1", got)
	}
	if got := attrs["vedetta.label"].AsString(); got != "person" {
		t.Errorf("vedetta.label = %q, want person", got)
	}
	if got := attrs["vedetta.track_id"].AsInt64(); got != 7 {
		t.Errorf("vedetta.track_id = %d, want 7", got)
	}
	if got := attrs["vedetta.event_id"].AsString(); got != "cam1-t7-123" {
		t.Errorf("vedetta.event_id = %q, want cam1-t7-123", got)
	}
	if got := attrs["vedetta.zone"].AsString(); got != "driveway" {
		t.Errorf("vedetta.zone = %q, want driveway", got)
	}
	// Score is stored as float32 in camera.Event and widened to float64 on the
	// span; compare against the same widening to avoid float32->float64 precision
	// mismatch (float64(float32(0.91)) != 0.91).
	if got, want := attrs["vedetta.score"].AsFloat64(), float64(float32(0.91)); got != want {
		t.Errorf("vedetta.score = %v, want %v", got, want)
	}
}

func TestRunEventLoopTracingEnd(t *testing.T) {
	tracer, sr := newTestTracer()
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sub := testSubsystems()
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runEventLoop(ctx, cfg, db, sub, nil, tracer)

	ev := camera.Event{
		ID:         "cam1-t7-456",
		CameraName: "cam1",
		Label:      "person",
		Score:      0.8,
		TrackID:    7,
		Timestamp:  time.Now(),
	}
	sub.events <- ev

	// Gate: only send EventEnd once the root span is recorded. The event-loop
	// is a single goroutine processing one select case per iteration, so once
	// the root span has ended, the active-map insert that follows it completes
	// before the loop can process the EventEnd we send next. This avoids the
	// race where a simultaneously-ready EventEnd is handled before the event is
	// tracked.
	waitForSpan(t, sr, "event")

	sub.eventEnds <- camera.EventEnd{
		EventID:    ev.ID,
		CameraName: "cam1",
		EndTime:    time.Now(),
	}

	waitForSpan(t, sr, "event.end")

	spans := sr.Ended()
	root := spanByName(spans, "event")
	endSpan := spanByName(spans, "event.end")
	if root == nil || endSpan == nil {
		t.Fatalf("missing spans: event=%v event.end=%v", root != nil, endSpan != nil)
	}
	if endSpan.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Errorf("event.end parent = %v, want root %v", endSpan.Parent().SpanID(), root.SpanContext().SpanID())
	}
	if endSpan.SpanContext().TraceID() != root.SpanContext().TraceID() {
		t.Errorf("event.end trace id != root trace id")
	}
}

type stubClipSaver struct{ err error }

func (s stubClipSaver) SaveClip(ctx context.Context, ev camera.Event) error { return s.err }

func TestExtractClipSpanSuccess(t *testing.T) {
	tracer, sr := newTestTracer()
	err := extractClipSpan(context.Background(), tracer, stubClipSaver{}, camera.Event{ID: "e1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	span := spanByName(sr.Ended(), "clip.extract")
	if span == nil {
		t.Fatal("clip.extract span not recorded")
	}
	if span.Status().Code == codes.Error {
		t.Errorf("status = Error, want unset on success")
	}
}

func TestExtractClipSpanError(t *testing.T) {
	tracer, sr := newTestTracer()
	wantErr := errors.New("clip not ready")
	err := extractClipSpan(context.Background(), tracer, stubClipSaver{err: wantErr}, camera.Event{ID: "e2"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	span := spanByName(sr.Ended(), "clip.extract")
	if span == nil {
		t.Fatal("clip.extract span not recorded")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("status = %v, want Error", span.Status().Code)
	}
	if span.Status().Description != "save clip" {
		t.Errorf("status description = %q, want %q", span.Status().Description, "save clip")
	}
	events := span.Events()
	if len(events) == 0 || events[0].Name != "exception" {
		t.Errorf("expected exception event from RecordError, got %v", events)
	}
}
