package tracing

import (
	"errors"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		env     map[string]string
		want    resolvedEndpoint
		wantErr bool
	}{
		{
			name: "scheme-less host:port uses WithEndpoint + insecure",
			cfg:  Config{Endpoint: "otel.lan:4318", Insecure: true},
			want: resolvedEndpoint{Value: "otel.lan:4318", AsURL: false, Insecure: true},
		},
		{
			name: "http URL uses WithEndpointURL",
			cfg:  Config{Endpoint: "http://otel.lan:4318", Insecure: true},
			want: resolvedEndpoint{Value: "http://otel.lan:4318", AsURL: true},
		},
		{
			name: "https URL uses WithEndpointURL",
			cfg:  Config{Endpoint: "https://otel.example.com", Insecure: true},
			want: resolvedEndpoint{Value: "https://otel.example.com", AsURL: true},
		},
		{
			name: "empty config falls back to env",
			cfg:  Config{},
			env:  map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://envhost:4318"},
			want: resolvedEndpoint{Value: "http://envhost:4318", AsURL: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveEndpoint(tc.cfg, env(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveEndpointMissing(t *testing.T) {
	_, err := resolveEndpoint(Config{}, env(nil))
	if !errors.Is(err, errNoEndpoint) {
		t.Fatalf("err = %v, want errNoEndpoint", err)
	}
}
