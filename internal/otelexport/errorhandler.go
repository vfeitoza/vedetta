package otelexport

import (
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

// rateLimitedErrorHandler logs OTel export errors at most once per interval so a
// down OTLP backend cannot spam the log with one line per dropped span or log
// record. now is injected for deterministic tests; production uses time.Now.
type rateLimitedErrorHandler struct {
	mu       sync.Mutex
	interval time.Duration
	now      func() time.Time
	last     time.Time
}

func (h *rateLimitedErrorHandler) Handle(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if now.Sub(h.last) < h.interval {
		return
	}
	h.last = now
	slog.Warn("OTLP export error (rate-limited)", "err", err)
}

var errorHandlerOnce sync.Once

// InstallRateLimitedErrorHandler installs a process-global OpenTelemetry error
// handler that coalesces export errors to at most one log line per interval. The
// OTel error handler is a single global shared by every signal (traces, logs);
// this installs it exactly once regardless of how many subsystems call it, so a
// down collector cannot spam stderr and the tracing and logging arms never
// clobber each other's handler.
func InstallRateLimitedErrorHandler(interval time.Duration) {
	errorHandlerOnce.Do(func() {
		otel.SetErrorHandler(&rateLimitedErrorHandler{interval: interval, now: time.Now})
	})
}
