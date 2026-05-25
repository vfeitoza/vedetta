package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/recording"
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

// countEnded returns how many ended spans carry the given name.
func countEnded(sr *tracetest.SpanRecorder, name string) int {
	n := 0
	for _, s := range sr.Ended() {
		if s.Name() == name {
			n++
		}
	}
	return n
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

// pollEnqueued waits up to ~1s for the fake enqueuer to receive n events. The
// emit work runs on a detached goroutine, so the assertion must poll.
func pollEnqueued(t *testing.T, enq *fakeEnqueuer, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if enq.count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("enqueuer received %d events, want %d", enq.count(), n)
}

func TestRunEventLoopEnqueuesOnSuccess(t *testing.T) {
	tracer, sr := newTestTracer()
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sub := testSubsystems()
	enq := &fakeEnqueuer{}
	sub.notifier = enq
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runEventLoop(ctx, cfg, db, sub, nil, tracer)

	// No snapshot image and nil MQTT client: the emit goroutine skips
	// snapshot.save and mqtt.publish and proceeds straight to Enqueue.
	sub.events <- camera.Event{
		ID:         "cam1-t9-1",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  time.Now(),
	}

	waitForSpan(t, sr, "event")
	pollEnqueued(t, enq, 1)
	if got := enq.at(0).ID; got != "cam1-t9-1" {
		t.Errorf("enqueued event ID = %q, want cam1-t9-1", got)
	}
}

func TestRunEventLoopSkipsEnqueueOnDBFailure(t *testing.T) {
	tracer, sr := newTestTracer()
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Close the DB so SaveEvent fails; the loop must not spawn the emit goroutine.
	_ = db.Close()

	sub := testSubsystems()
	enq := &fakeEnqueuer{}
	sub.notifier = enq
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runEventLoop(ctx, cfg, db, sub, nil, tracer)

	sub.events <- camera.Event{
		ID:         "cam1-t9-2",
		CameraName: "cam1",
		Label:      "person",
		Timestamp:  time.Now(),
	}

	waitForSpan(t, sr, "event")
	dbSpan := spanByName(sr.Ended(), "db.save_event")
	if dbSpan == nil || dbSpan.Status().Code != codes.Error {
		t.Fatalf("expected db.save_event with Error status on closed DB")
	}
	// Give any (erroneously spawned) goroutine a chance to run, then assert none.
	time.Sleep(100 * time.Millisecond)
	if enq.count() != 0 {
		t.Errorf("enqueuer received %d events on db failure, want 0", enq.count())
	}
}

// TestRunEventLoopDoesNotBlockOnSlowEmit is the regression guard for the async
// offload: the per-event snapshot/MQTT/push work must run on a detached goroutine
// and never serialize the event loop. A slow enqueuer (emitDelay per event)
// stands in for that work. Because the loop closes the root "event" span inline,
// immediately after dispatching the emit goroutine, it must drain a burst of n
// events almost instantly - well inside drainBudget, which is a small fraction of
// n*emitDelay. Under the pre-offload inline model the loop would take ~n*emitDelay
// to drain (the regression this guards), failing the budget assertion.
func TestRunEventLoopDoesNotBlockOnSlowEmit(t *testing.T) {
	const (
		n           = 10
		emitDelay   = 100 * time.Millisecond
		drainBudget = 300 * time.Millisecond // serial path would need ~n*emitDelay = 1s
	)
	tracer, sr := newTestTracer()
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sub := testSubsystems()
	sub.events = make(chan camera.Event, n) // buffer the whole burst so sends don't throttle
	enq := &slowEnqueuer{delay: emitDelay}
	sub.notifier = enq
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runEventLoop(ctx, cfg, db, sub, nil, tracer)

	// No snapshot image and nil MQTT client: each emit goroutine skips
	// snapshot.save and mqtt.publish and goes straight to the slow Enqueue, so the
	// only emit cost is the injected delay on the detached goroutine.
	start := time.Now()
	for i := 0; i < n; i++ {
		sub.events <- camera.Event{
			ID:         fmt.Sprintf("cam1-burst-%d", i),
			CameraName: "cam1",
			Label:      "person",
			Timestamp:  time.Now(),
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for countEnded(sr, "event") < n && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	drain := time.Since(start)
	if got := countEnded(sr, "event"); got < n {
		t.Fatalf("loop closed %d/%d root spans within deadline", got, n)
	}
	if drain >= drainBudget {
		t.Fatalf("loop took %v to drain %d events (budget %v): emit work is blocking the loop", drain, n, drainBudget)
	}

	// The work is detached, not dropped: all n enqueues eventually complete. They
	// run concurrently, so they finish in roughly one emitDelay rather than
	// n*emitDelay - additional evidence the loop fanned them out in parallel.
	for enq.completed() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := enq.completed(); got != n {
		t.Fatalf("enqueuer completed %d/%d events: detached emit work was dropped", got, n)
	}
}

type stubClipSaver struct {
	err   error
	stats recording.ClipStats
}

func (s stubClipSaver) SaveClip(ctx context.Context, ev camera.Event) (recording.ClipStats, error) {
	return s.stats, s.err
}

func spanAttrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestExtractClipSpanSuccess(t *testing.T) {
	tracer, sr := newTestTracer()
	err := extractClipSpan(context.Background(), tracer, stubClipSaver{}, camera.Event{ID: "e1"}, 1)
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

// The clip.extract span carries the attempt number plus the extraction stats
// (segment count, output size, window duration) and the camera/label, so a slow
// or failed extraction can be diagnosed from the trace alone.
func TestExtractClipSpanRecordsStats(t *testing.T) {
	tracer, sr := newTestTracer()
	saver := stubClipSaver{stats: recording.ClipStats{
		SegmentCount: 3,
		OutputBytes:  4096,
		ClipDuration: 2 * time.Second,
	}}
	ev := camera.Event{ID: "e3", CameraName: "garage", Label: "person"}

	if err := extractClipSpan(context.Background(), tracer, saver, ev, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	span := spanByName(sr.Ended(), "clip.extract")
	if span == nil {
		t.Fatal("clip.extract span not recorded")
	}
	attrs := spanAttrs(span)
	if got := attrs["clip.attempt"].AsInt64(); got != 2 {
		t.Errorf("clip.attempt = %d, want 2", got)
	}
	if got := attrs["clip.segment_count"].AsInt64(); got != 3 {
		t.Errorf("clip.segment_count = %d, want 3", got)
	}
	if got := attrs["clip.output_bytes"].AsInt64(); got != 4096 {
		t.Errorf("clip.output_bytes = %d, want 4096", got)
	}
	if got := attrs["clip.duration_ms"].AsInt64(); got != 2000 {
		t.Errorf("clip.duration_ms = %d, want 2000", got)
	}
	if got := attrs["vedetta.camera"].AsString(); got != "garage" {
		t.Errorf("vedetta.camera = %q, want garage", got)
	}
	if got := attrs["vedetta.label"].AsString(); got != "person" {
		t.Errorf("vedetta.label = %q, want person", got)
	}
}

func TestExtractClipSpanError(t *testing.T) {
	tracer, sr := newTestTracer()
	wantErr := errors.New("clip not ready")
	// Even on failure the attempt number is recorded so a transient early-attempt
	// error reads differently from a permanent final-attempt loss.
	err := extractClipSpan(context.Background(), tracer, stubClipSaver{err: wantErr}, camera.Event{ID: "e2"}, 5)
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
	if got := spanAttrs(span)["clip.attempt"].AsInt64(); got != 5 {
		t.Errorf("clip.attempt = %d, want 5 on the failing attempt", got)
	}
	events := span.Events()
	if len(events) == 0 || events[0].Name != "exception" {
		t.Errorf("expected exception event from RecordError, got %v", events)
	}
}
