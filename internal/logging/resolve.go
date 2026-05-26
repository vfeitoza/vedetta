package logging

import (
	"strings"

	"github.com/rvben/vedetta/internal/otelexport"
)

// Config mirrors config.LoggingConfig plus the tracing endpoint fallback. Kept
// here so the logging package does not import config.
type Config struct {
	Enabled          bool
	Endpoint         string
	Protocol         string
	Insecure         bool
	ServiceName      string
	FallbackEndpoint string // tracing endpoint, used when Endpoint is empty
}

// resolveEndpoint decides the explicit OTLP endpoint for logs. It only ever
// promotes a config-supplied endpoint (the logging endpoint, then the tracing
// fallback) to an explicit exporter option. When neither is set it returns
// explicit=false, so Init builds the exporter without an endpoint option and
// the SDK applies its own env precedence (OTEL_EXPORTER_OTLP_LOGS_ENDPOINT over
// OTEL_EXPORTER_OTLP_ENDPOINT). The generic env is never read here, because
// doing so and passing it explicitly would override the SDK's signal-specific
// precedence. Config/fallback values are trimmed so a whitespace endpoint
// defers to env rather than becoming a bogus explicit option.
func resolveEndpoint(cfg Config) (ep otelexport.Endpoint, explicit bool) {
	raw := strings.TrimSpace(cfg.Endpoint)
	if raw == "" {
		raw = strings.TrimSpace(cfg.FallbackEndpoint)
	}
	if raw == "" {
		return otelexport.Endpoint{}, false
	}
	return otelexport.Classify(raw, cfg.Insecure), true
}

// resolveProtocol picks the OTLP transport for logs. The protocol selects the
// exporter constructor, so it must be decided up front. Logs-specific env wins
// over the generic env, per the OTLP spec; the default is http/protobuf.
func resolveProtocol(cfg Config, getenv func(string) string) string {
	if p := otelexport.ParseProtocol(cfg.Protocol); p != "" {
		return p
	}
	if p := otelexport.ParseProtocol(getenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL")); p != "" {
		return p
	}
	if p := otelexport.ParseProtocol(getenv("OTEL_EXPORTER_OTLP_PROTOCOL")); p != "" {
		return p
	}
	return "http/protobuf"
}
