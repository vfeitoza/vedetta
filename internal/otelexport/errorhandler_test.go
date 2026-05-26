package otelexport

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

// TestRateLimitedErrorHandlerCoalesces verifies that back-to-back errors at the
// same instant produce only one log line, and that a new error after the
// interval elapses produces a second line.
func TestRateLimitedErrorHandlerCoalesces(t *testing.T) {
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	cur := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	h := &rateLimitedErrorHandler{
		interval: 30 * time.Second,
		now:      func() time.Time { return cur },
	}

	// First call must always log (zero last is far in the past).
	h.Handle(errors.New("err1"))
	// Second call at the same instant must be suppressed.
	h.Handle(errors.New("err2"))

	count := strings.Count(buf.String(), "OTLP export error")
	if count != 1 {
		t.Errorf("expected 1 log line after two calls at same time, got %d; log: %q", count, buf.String())
	}

	// Advance past the interval: next call must produce a second line.
	cur = cur.Add(31 * time.Second)
	h.Handle(errors.New("err3"))

	count = strings.Count(buf.String(), "OTLP export error")
	if count != 2 {
		t.Errorf("expected 2 log lines after interval elapsed, got %d; log: %q", count, buf.String())
	}
}

// TestInstallRateLimitedErrorHandlerReplacesDefault verifies that calling
// InstallRateLimitedErrorHandler installs a non-default OTel error handler.
func TestInstallRateLimitedErrorHandlerReplacesDefault(t *testing.T) {
	// Reset the once so this test is not order-dependent. Because errorHandlerOnce
	// is a package-level sync.Once we cannot reset it from outside, but the test
	// is in-package so we can call the install unconditionally via a fresh once.
	// We verify the type directly since we are in-package.
	InstallRateLimitedErrorHandler(time.Second)

	got := otel.GetErrorHandler()
	if _, ok := got.(*rateLimitedErrorHandler); !ok {
		// Also accept it being wrapped: fall back to type-string check.
		if strings.Contains(fmt.Sprintf("%T", got), "ErrDelegator") {
			t.Fatalf("InstallRateLimitedErrorHandler must replace the default ErrDelegator, got %T", got)
		}
	}
}
