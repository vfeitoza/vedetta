package api

import (
	"net/http"
	"testing"
)

// The HTTP server must bound how long a client may take to send a request so a
// slowloris client cannot pin connections open indefinitely. ReadHeaderTimeout
// covers slow headers; ReadTimeout covers a slow request body. WriteTimeout
// must stay zero: SSE (/api/events/stream), MSE, and HLS responses stream for
// minutes and a global write deadline would sever them mid-stream.
func TestHTTPServerTimeoutsMitigateSlowloris(t *testing.T) {
	srv, _ := newTestServer(t)

	h := srv.buildHTTPServer(":0", http.NewServeMux())

	if h.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout must be set (slow-headers slowloris), got %v", h.ReadHeaderTimeout)
	}
	if h.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout must be set (slow-body slowloris), got %v", h.ReadTimeout)
	}
	if h.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout must be set, got %v", h.IdleTimeout)
	}
	if h.WriteTimeout != 0 {
		t.Errorf("WriteTimeout must stay 0 so streaming endpoints (SSE/MSE/HLS) are not cut off, got %v", h.WriteTimeout)
	}
}
