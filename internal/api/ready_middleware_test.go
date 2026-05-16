package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadyMiddleware_HealthProbesBypassReadinessGate proves the liveness and
// readiness probes reach their own handlers even before the server is ready.
//
// readyMiddleware blanket-503s every /api/* path until s.ready is set. The
// monitoring stack scrapes /api/health/live for the ServiceDown alert, so if
// the gate shadows it every restart flaps a spurious outage alert even though
// the process is perfectly alive. Liveness must answer "is the process alive",
// never "is it finished initializing". Readiness must return its own
// structured payload (so callers can see *why* it is not ready), not the
// generic middleware placeholder.
func TestReadyMiddleware_HealthProbesBypassReadinessGate(t *testing.T) {
	srv, _ := newTestServer(t)

	// Reproduce the startup window: production constructs the server with
	// ready=false and flips it true only after subsystems initialize
	// (server.go s.ready.Store(true)). This is the window the gate guards.
	srv.ready.Store(false)

	// Exact production wrapping order around the real mux.
	handler := srv.readyMiddleware(authMiddleware(srv, apiBodyLimitMiddleware(srv.mux)))

	t.Run("liveness is 200 while not ready", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health/live", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("liveness must be 200 while initializing, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("liveness body not JSON: %v (%s)", err, rec.Body.String())
		}
		if body["status"] != "ok" {
			t.Fatalf("liveness status = %v, want ok (got generic gate body? %s)", body["status"], rec.Body.String())
		}
	})

	t.Run("readiness returns its own payload while not ready", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health/ready", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("readiness must be 503 while initializing, got %d: %s", rec.Code, rec.Body.String())
		}
		// The real GetHealthReady payload has a "checks" object with
		// "initialized"; the generic gate body does not.
		if strings.Contains(rec.Body.String(), "Vedetta is initializing") {
			t.Fatalf("readiness shadowed by generic gate body: %s", rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("readiness body not JSON: %v (%s)", err, rec.Body.String())
		}
		checks, ok := body["checks"].(map[string]any)
		if !ok {
			t.Fatalf("readiness body missing checks object: %s", rec.Body.String())
		}
		if _, ok := checks["initialized"]; !ok {
			t.Fatalf("readiness checks missing initialized: %s", rec.Body.String())
		}
	})

	t.Run("non-probe API paths are still gated", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("non-probe API path must stay gated while not ready, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "initializing") {
			t.Fatalf("expected generic gate body for gated path, got: %s", rec.Body.String())
		}
	})
}
