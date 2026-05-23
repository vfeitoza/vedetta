package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newTestTracerProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return exp
}

func TestWithTracingFiltersNoisyEndpoints(t *testing.T) {
	exp := newTestTracerProvider(t)
	s := &Server{tracingEnabled: true}
	h := s.withTracing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{
		"/metrics",
		"/api/health",
		"/api/health/live",
		"/api/health/ready",
		"/api/cameras/front/detections",
		"/api/cameras/front/mse/ws",
		"/api/cameras/front/webrtc",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if n := len(exp.GetSpans()); n != 0 {
		t.Fatalf("filtered endpoints produced %d spans, want 0", n)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/cameras", nil))
	if n := len(exp.GetSpans()); n != 1 {
		t.Fatalf("normal route produced %d spans, want 1", n)
	}
}

func TestWithTracingDisabledNoSpans(t *testing.T) {
	exp := newTestTracerProvider(t)
	s := &Server{tracingEnabled: false}
	h := s.withTracing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/cameras", nil))
	if n := len(exp.GetSpans()); n != 0 {
		t.Fatalf("disabled tracing produced %d spans, want 0", n)
	}
}
