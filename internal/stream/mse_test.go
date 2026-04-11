package stream

import (
	"net/http/httptest"
	"testing"
)

func TestOriginAllowed_SameHost(t *testing.T) {
	req := httptest.NewRequest("GET", "http://vedetta.local/api/cameras/front/mse/ws", nil)
	req.Host = "vedetta.local"
	req.Header.Set("Origin", "http://vedetta.local")

	if !originAllowed(req, nil, nil) {
		t.Fatal("expected same-host origin to be allowed")
	}
}

func TestOriginAllowed_ExplicitAllowlist(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/cameras/front/mse/ws", nil)
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "https://app.example.com")

	if !originAllowed(req, []string{"https://app.example.com"}, nil) {
		t.Fatal("expected allowlisted origin to be allowed")
	}
}

func TestOriginAllowed_RejectsMismatchedOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "http://vedetta.local/api/cameras/front/mse/ws", nil)
	req.Host = "vedetta.local"
	req.Header.Set("Origin", "https://evil.example.com")

	if originAllowed(req, nil, nil) {
		t.Fatal("expected mismatched origin to be rejected")
	}
}

func TestOriginAllowed_TrustedProxyHTTPS(t *testing.T) {
	// Caddy-style reverse proxy: browser → Caddy (https://vedetta.am8.nl) → vedetta (plain HTTP).
	// Caddy forwards the original Host header and sets X-Forwarded-Proto=https.
	// Without trusted-proxy awareness, vedetta would treat the scheme as http and reject.
	req := httptest.NewRequest("GET", "http://vedetta.am8.nl/api/cameras/front/mse/ws", nil)
	req.Host = "vedetta.am8.nl"
	req.RemoteAddr = "10.10.30.10:43210"
	req.Header.Set("Origin", "https://vedetta.am8.nl")
	req.Header.Set("X-Forwarded-Proto", "https")

	trusted := parseTrustedProxies([]string{"10.10.30.10/32"})
	if !originAllowed(req, nil, trusted) {
		t.Fatal("expected origin from trusted proxy with X-Forwarded-Proto=https to be allowed")
	}
}

func TestOriginAllowed_UntrustedProxyCannotForgeScheme(t *testing.T) {
	// A random client claiming X-Forwarded-Proto=https must not bypass the origin check.
	req := httptest.NewRequest("GET", "http://vedetta.am8.nl/api/cameras/front/mse/ws", nil)
	req.Host = "vedetta.am8.nl"
	req.RemoteAddr = "198.51.100.7:55555"
	req.Header.Set("Origin", "https://vedetta.am8.nl")
	req.Header.Set("X-Forwarded-Proto", "https")

	trusted := parseTrustedProxies([]string{"10.10.30.10/32"})
	if originAllowed(req, nil, trusted) {
		t.Fatal("untrusted client must not be able to spoof X-Forwarded-Proto")
	}
}
