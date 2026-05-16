package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// staticTestFS mirrors the kinds of assets the real embedded FS carries: a
// frequently-redeployed app shell, a stylesheet, and a vendored library.
func staticTestFS() fstest.MapFS {
	return fstest.MapFS{
		"app.js":     {Data: []byte("console.log('v1');\n")},
		"style.css":  {Data: []byte("body{margin:0}\n")},
		"hls.min.js": {Data: []byte("/* hls.js bundle */\n")},
		"page.html":  {Data: []byte("<!doctype html><title>v1</title>")},
	}
}

func staticTestHandler() http.Handler {
	fsys := staticTestFS()
	return staticFileHandler(fsys, http.FileServer(http.FS(fsys)))
}

func bodyETag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func TestStaticHandlerEmitsRevalidatingETag(t *testing.T) {
	h := staticTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app.js: got %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("app.js Cache-Control = %q, want no-cache (forces revalidation so a deploy reaches the device)", cc)
	}
	want := bodyETag([]byte("console.log('v1');\n"))
	if got := rec.Header().Get("ETag"); got != want {
		t.Errorf("app.js ETag = %q, want content hash %q", got, want)
	}
}

func TestStaticHandlerReturns304OnMatchingETag(t *testing.T) {
	h := staticTestHandler()

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response carried no ETag to revalidate against")
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional GET: got %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 response must have an empty body, got %d bytes", rec.Body.Len())
	}
	if rec.Header().Get("ETag") != etag {
		t.Errorf("304 must echo the ETag, got %q want %q", rec.Header().Get("ETag"), etag)
	}
}

func TestStaticHandlerServesFreshBodyWhenETagStale(t *testing.T) {
	h := staticTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("If-None-Match", `"deadbeefdeadbeefdeadbeefdeadbeef"`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stale If-None-Match: got %d, want 200 (full body)", rec.Code)
	}
	if rec.Body.String() != "console.log('v1');\n" {
		t.Errorf("expected fresh app.js body, got %q", rec.Body.String())
	}
}

func TestStaticHandlerCachePolicyByAsset(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/app.js", "no-cache"},
		{"/page.html", "no-cache"},
		{"/style.css", "no-cache"},
		{"/hls.min.js", "public, max-age=31536000, immutable"},
	}
	h := staticTestHandler()
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200", tc.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestETagMatches(t *testing.T) {
	const etag = `"abc123"`
	cases := []struct {
		ifNoneMatch string
		want        bool
	}{
		{`"abc123"`, true},
		{`W/"abc123"`, true},
		{`"x", "abc123"`, true},
		{`*`, true},
		{`"nope"`, false},
		{``, false},
		{`"abc1234"`, false},
	}
	for _, tc := range cases {
		if got := etagMatches(tc.ifNoneMatch, etag); got != tc.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", tc.ifNoneMatch, etag, got, tc.want)
		}
	}
}
