package tracing

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var errNoEndpoint = errors.New("tracing: no OTLP endpoint configured")

// resolvedEndpoint describes how the OTLP exporter is addressed, independent of
// transport. When AsURL is true, Value is a full URL passed to WithEndpointURL
// (its scheme decides plaintext vs TLS). Otherwise Value is a scheme-less
// host:port passed to WithEndpoint, and Insecure selects plaintext.
type resolvedEndpoint struct {
	Value    string
	AsURL    bool
	Insecure bool
}

// resolveEndpoint applies config-then-env precedence and classifies the
// endpoint form. getenv is injected for testability.
func resolveEndpoint(cfg Config, getenv func(string) string) (resolvedEndpoint, error) {
	ep := strings.TrimSpace(cfg.Endpoint)
	if ep == "" {
		ep = strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if ep == "" {
		return resolvedEndpoint{}, errNoEndpoint
	}
	if strings.Contains(ep, "://") {
		return resolvedEndpoint{Value: ep, AsURL: true}, nil
	}
	return resolvedEndpoint{Value: ep, AsURL: false, Insecure: cfg.Insecure}, nil
}

// resolveProtocol applies config-then-env precedence for the OTLP transport.
func resolveProtocol(cfg Config, getenv func(string) string) string {
	p := strings.ToLower(strings.TrimSpace(cfg.Protocol))
	if p == "" {
		p = strings.ToLower(strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	}
	return p
}

// buildExporter constructs the OTLP span exporter for the configured transport
// and endpoint.
func buildExporter(ctx context.Context, cfg Config, getenv func(string) string) (sdktrace.SpanExporter, error) {
	re, err := resolveEndpoint(cfg, getenv)
	if err != nil {
		return nil, err
	}
	if resolveProtocol(cfg, getenv) == "grpc" {
		var opts []otlptracegrpc.Option
		if re.AsURL {
			opts = append(opts, otlptracegrpc.WithEndpointURL(re.Value))
		} else {
			opts = append(opts, otlptracegrpc.WithEndpoint(re.Value))
			if re.Insecure {
				opts = append(opts, otlptracegrpc.WithInsecure())
			}
		}
		return otlptracegrpc.New(ctx, opts...)
	}
	// default: OTLP/HTTP ("http", "http/protobuf", or unset)
	var opts []otlptracehttp.Option
	if re.AsURL {
		opts = append(opts, otlptracehttp.WithEndpointURL(re.Value))
	} else {
		opts = append(opts, otlptracehttp.WithEndpoint(re.Value))
		if re.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	}
	return otlptracehttp.New(ctx, opts...)
}
