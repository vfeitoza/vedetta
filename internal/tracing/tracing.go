// Package tracing owns vedetta's optional OpenTelemetry distributed tracing.
// It is opt-in and default-off: when disabled, Init installs a no-op tracer
// with no exporter and no overhead, so call sites use the returned Tracer
// unconditionally.
package tracing

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Config mirrors config.TracingConfig (identical field set so the caller can
// convert directly). Kept here so the tracing package does not import config.
type Config struct {
	Enabled     bool
	Endpoint    string
	Protocol    string
	Insecure    bool
	ServiceName string
}

const instrumentationScope = "github.com/rvben/vedetta"

// Provider holds the active tracer and shutdown hooks. The zero value is not
// usable; obtain one from Init.
type Provider struct {
	tracer   trace.Tracer
	shutdown []func(context.Context) error
}

// Tracer returns the tracer to use for span creation. It is always non-nil; a
// no-op tracer when tracing is disabled or failed to initialize.
func (p *Provider) Tracer() trace.Tracer { return p.tracer }

// Shutdown flushes buffered spans and runs all shutdown hooks. Bound the
// context with a timeout at the call site so a blocked exporter cannot wedge
// process exit.
func (p *Provider) Shutdown(ctx context.Context) error {
	var err error
	for _, fn := range p.shutdown {
		if e := fn(ctx); e != nil {
			err = errors.Join(err, e)
		}
	}
	return err
}

func noopProvider() *Provider {
	return &Provider{tracer: tracenoop.NewTracerProvider().Tracer(instrumentationScope)}
}

// Init configures tracing per cfg. It never returns an error that should be
// fatal: construction/config failures log a warning and degrade to a no-op
// provider so the process still starts and serves. It installs process-global
// OpenTelemetry state and is intended to be called at most once per process; a
// repeated call replaces the global provider, but tracers obtained before it
// retain their original delegate.
func Init(ctx context.Context, cfg Config, version string) (*Provider, error) {
	if !cfg.Enabled {
		return noopProvider(), nil
	}

	exp, err := buildExporter(ctx, cfg, os.Getenv)
	if err != nil {
		slog.Warn("tracing disabled: OTLP exporter init failed", "err", err)
		return noopProvider(), nil
	}

	name := cfg.ServiceName
	if name == "" {
		name = "vedetta"
	}
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(name),
		semconv.ServiceVersion(version),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(newSampler()),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(newRateLimitedErrorHandler(30 * time.Second))

	slog.Info("tracing enabled", "endpoint", cfg.Endpoint, "protocol", cfg.Protocol)
	return &Provider{
		tracer:   tp.Tracer(instrumentationScope),
		shutdown: []func(context.Context) error{tp.Shutdown},
	}, nil
}

// rateLimitedErrorHandler logs OTel export errors at most once per interval so
// a down backend cannot spam the log with one line per dropped span.
type rateLimitedErrorHandler struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newRateLimitedErrorHandler(interval time.Duration) *rateLimitedErrorHandler {
	return &rateLimitedErrorHandler{interval: interval}
}

func (h *rateLimitedErrorHandler) Handle(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if now.Sub(h.last) < h.interval {
		return
	}
	h.last = now
	slog.Warn("tracing export error (rate-limited)", "err", err)
}
