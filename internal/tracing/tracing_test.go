package tracing

import (
	"context"
	"testing"
)

func TestInitDisabledReturnsNoopTracer(t *testing.T) {
	p, err := Init(context.Background(), Config{Enabled: false}, "test")
	if err != nil {
		t.Fatalf("Init err = %v", err)
	}
	if p == nil || p.Tracer() == nil {
		t.Fatal("expected non-nil provider and tracer")
	}
	_, span := p.Tracer().Start(context.Background(), "x")
	if span.IsRecording() {
		t.Error("disabled tracer must produce non-recording spans")
	}
	span.End()
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown err = %v", err)
	}
}

func TestInitEnabledNoEndpointFallsBackToNoop(t *testing.T) {
	// Enabled but no endpoint configured and none in env: must not error, must
	// return a no-op provider so the NVR still starts.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	p, err := Init(context.Background(), Config{Enabled: true}, "test")
	if err != nil {
		t.Fatalf("Init err = %v, want nil (graceful fallback)", err)
	}
	_, span := p.Tracer().Start(context.Background(), "x")
	if span.IsRecording() {
		t.Error("fallback tracer must produce non-recording spans")
	}
	span.End()
}
