package api

import (
	"crypto/tls"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestContentSecurityPolicy_DisablesInlineScriptAttributes(t *testing.T) {
	csp := contentSecurityPolicy()
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP unexpectedly allows inline scripts: %s", csp)
	}
	if !strings.Contains(csp, "script-src-attr 'none'") {
		t.Fatalf("CSP missing script-src-attr restriction: %s", csp)
	}
	if !strings.Contains(csp, "https://unpkg.com") {
		t.Fatalf("CSP missing allowed external HTMX origin: %s", csp)
	}
	if !strings.Contains(csp, "'sha256-") {
		t.Fatalf("CSP missing inline script hashes: %s", csp)
	}
}

func TestContentSecurityPolicy_ConnectSrcRestrictedToSelf(t *testing.T) {
	csp := contentSecurityPolicy()

	var connectSrc string
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "connect-src") {
			connectSrc = directive
			break
		}
	}
	if connectSrc == "" {
		t.Fatalf("CSP missing connect-src directive: %s", csp)
	}
	if connectSrc != "connect-src 'self'" {
		t.Fatalf("connect-src must be restricted to 'self', got %q", connectSrc)
	}
}

func TestSecurityHeadersMiddleware_HSTSOnlyOnSecureRequests(t *testing.T) {
	s := &Server{}
	handler := s.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	secureReq := httptest.NewRequest(http.MethodGet, "https://vedetta.example/", nil)
	secureReq.TLS = &tls.ConnectionState{}
	secureRec := httptest.NewRecorder()
	handler.ServeHTTP(secureRec, secureReq)
	if got := secureRec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("secure request HSTS header = %q, want %q", got, "max-age=31536000")
	}

	plainReq := httptest.NewRequest(http.MethodGet, "http://vedetta.example/", nil)
	plainRec := httptest.NewRecorder()
	handler.ServeHTTP(plainRec, plainReq)
	if got := plainRec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("plain HTTP request must not set HSTS, got %q", got)
	}
}

func TestStaticAssets_DoNotUseInlineEventHandlers(t *testing.T) {
	pattern := regexp.MustCompile(`\s(onclick|onchange|oninput|onfocus|onkeydown|onload|onerror|onsubmit)\s*=|hx-on::`)

	checkFile := func(path string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if pattern.Match(data) {
			t.Fatalf("found inline handler in %s", path)
		}
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	apiDir := filepath.Dir(thisFile)
	staticDir := filepath.Join(apiDir, "static")
	err := filepath.WalkDir(staticDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") {
			checkFile(path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk static dir: %v", err)
	}

	checkFile(filepath.Join(apiDir, "handler_partials.go"))
}
