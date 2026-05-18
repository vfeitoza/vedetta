package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// In full (non-setup) mode the discovery endpoints must be registered.
// They were previously only wired in NewSetupMode, so the runtime
// Add Camera flow had no backend.
//
// This asserts route REGISTRATION via the mux's pattern match, not handler
// behavior. We deliberately do NOT call ServeHTTP: GET /api/discover would
// run a real ~5s WS-Discovery scan, and GET /api/discover/thumbnail/{ip}
// legitimately returns 404 until a thumbnail is cached -- so a status-code
// assertion would be both slow and wrong. http.ServeMux.Handler returns the
// matched pattern ("" when nothing matches), which is the precise signal.
func TestDiscoveryRoutesRegisteredInFullMode(t *testing.T) {
	s, _ := newTestServer(t) // existing helper: in-memory DB, empty camera mgr

	for _, route := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/discover"},
		{http.MethodPost, "/api/discover/probe"},
		{http.MethodGet, "/api/discover/thumbnail/192.168.1.10"},
	} {
		req := httptest.NewRequest(route.method, route.path, nil)
		_, pattern := s.mux.Handler(req)
		if pattern == "" {
			t.Errorf("%s %s did not match any registered route -- discovery not registered in full mode", route.method, route.path)
		}
	}
}
