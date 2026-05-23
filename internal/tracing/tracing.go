// Package tracing owns vedetta's optional OpenTelemetry distributed tracing.
// It is opt-in and default-off: when disabled, Init installs a no-op tracer
// with no exporter and no overhead, so call sites use the returned Tracer
// unconditionally.
package tracing

// Config mirrors config.TracingConfig (identical field set so the caller can
// convert directly). Kept here so the tracing package does not import config.
type Config struct {
	Enabled     bool
	Endpoint    string
	Protocol    string
	Insecure    bool
	SampleRatio float64
	ServiceName string
}
