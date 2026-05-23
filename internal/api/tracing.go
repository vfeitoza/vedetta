package api

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// shouldTraceRequest reports whether an inbound request should produce a span.
// High-frequency, low-value endpoints are excluded so a trace backend is not
// flooded and idle polling creates no span noise.
func shouldTraceRequest(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/metrics":
		return false
	case p == "/api/health", p == "/api/health/live", p == "/api/health/ready":
		return false
	case strings.HasPrefix(p, "/api/cameras/") && strings.HasSuffix(p, "/detections"):
		return false
	case strings.HasSuffix(p, "/mse/ws"), strings.HasSuffix(p, "/webrtc"):
		return false
	}
	return true
}

// withTracing wraps h with otelhttp request spans when tracing is enabled.
// When disabled it returns h unchanged so there is zero added overhead.
func (s *Server) withTracing(h http.Handler) http.Handler {
	if !s.tracingEnabled {
		return h
	}
	return otelhttp.NewHandler(h, "vedetta-api", otelhttp.WithFilter(shouldTraceRequest))
}
