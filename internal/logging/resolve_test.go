package logging

import (
	"testing"

	"github.com/rvben/vedetta/internal/otelexport"
)

func TestResolveEndpointPrefersConfigThenFallbackThenDefersToEnv(t *testing.T) {
	// Config endpoint wins.
	ep, explicit := resolveEndpoint(Config{Endpoint: "cfg:4318", FallbackEndpoint: "trace:4318", Insecure: true})
	if !explicit || ep != (otelexport.Endpoint{Value: "cfg:4318", Insecure: true}) {
		t.Fatalf("config endpoint must win: explicit=%v ep=%+v", explicit, ep)
	}
	// Empty config falls back to the tracing endpoint.
	ep, explicit = resolveEndpoint(Config{FallbackEndpoint: "trace:4318", Insecure: true})
	if !explicit || ep.Value != "trace:4318" {
		t.Fatalf("must fall back to tracing endpoint: explicit=%v ep=%+v", explicit, ep)
	}
	// Both empty: no explicit endpoint (defer to exporter env).
	_, explicit = resolveEndpoint(Config{})
	if explicit {
		t.Fatal("no config/fallback endpoint must yield explicit=false (defer to SDK env)")
	}
	// Whitespace-only endpoint is treated as empty (defer to env), not promoted
	// to a bogus explicit option.
	_, explicit = resolveEndpoint(Config{Endpoint: "   ", FallbackEndpoint: "  "})
	if explicit {
		t.Fatal("whitespace endpoint must yield explicit=false")
	}
}

func TestResolveProtocolPrefersConfigThenLogsEnvThenGenericThenDefault(t *testing.T) {
	get := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	// Config wins.
	if got := resolveProtocol(Config{Protocol: "GRPC"}, get(nil)); got != "grpc" {
		t.Errorf("config protocol must win: %q", got)
	}
	// Logs-specific env beats generic.
	got := resolveProtocol(Config{}, get(map[string]string{
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc",
		"OTEL_EXPORTER_OTLP_PROTOCOL":      "http/protobuf",
	}))
	if got != "grpc" {
		t.Errorf("logs-specific protocol env must win: %q", got)
	}
	// Generic env when no logs-specific.
	got = resolveProtocol(Config{}, get(map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}))
	if got != "grpc" {
		t.Errorf("generic protocol env must apply: %q", got)
	}
	// Default.
	if got := resolveProtocol(Config{}, get(nil)); got != "http/protobuf" {
		t.Errorf("default protocol must be http/protobuf: %q", got)
	}
}

func TestResolveFallbackInheritsTracingTransport(t *testing.T) {
	noenv := func(string) string { return "" }

	// Falling back to a scheme-less tracing endpoint inherits the tracing
	// insecure flag, not the logging one.
	ep, explicit := resolveEndpoint(Config{
		FallbackEndpoint: "trace:4317",
		FallbackInsecure: true,
		Insecure:         false, // logging default; must NOT be used for the fallback
	})
	if !explicit || ep.Value != "trace:4317" || !ep.Insecure {
		t.Fatalf("fallback must inherit tracing insecure: explicit=%v ep=%+v", explicit, ep)
	}

	// Falling back to the tracing endpoint also reuses tracing's protocol, so a
	// gRPC collector isn't probed with HTTP.
	if got := resolveProtocol(Config{
		FallbackEndpoint: "trace:4317",
		FallbackProtocol: "grpc",
	}, noenv); got != "grpc" {
		t.Errorf("fallback must reuse tracing protocol: %q", got)
	}

	// When logging has its OWN endpoint, the tracing fallback protocol must NOT
	// leak in (it would mismatch logging's endpoint).
	if got := resolveProtocol(Config{
		Endpoint:         "http://logs:4318",
		FallbackProtocol: "grpc",
	}, noenv); got != "http/protobuf" {
		t.Errorf("own endpoint must not inherit tracing protocol: %q", got)
	}

	// An explicit logs protocol env still beats the tracing fallback protocol.
	get := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if got := resolveProtocol(Config{
		FallbackEndpoint: "trace:4317",
		FallbackProtocol: "grpc",
	}, get(map[string]string{"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/protobuf"})); got != "http/protobuf" {
		t.Errorf("logs env must override tracing fallback protocol: %q", got)
	}
}
