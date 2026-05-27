package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestInitSendsConfiguredHeaders(t *testing.T) {
	// Multi-tenant trace backends (e.g. Tempo/Grafana Cloud) require a tenant
	// header on every push. Configured headers must reach the OTLP receiver on
	// the export request, mirroring the logging exporter's behavior.
	gotHeader := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotHeader <- r.Header.Get("X-Scope-OrgID"):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := Init(context.Background(), Config{
		Enabled:     true,
		Endpoint:    srv.URL,
		Protocol:    "http/protobuf",
		ServiceName: "vedetta-test",
		Headers:     map[string]string{"X-Scope-OrgID": "vedetta"},
	}, "v9.9.9")
	if err != nil {
		t.Fatalf("Init returned %v", err)
	}

	_, span := p.Tracer().Start(context.Background(), "x")
	span.End()

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown returned %v", err)
	}

	select {
	case h := <-gotHeader:
		if h != "vedetta" {
			t.Errorf("X-Scope-OrgID header = %q, want \"vedetta\"", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no OTLP export received")
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
