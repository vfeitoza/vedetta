package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureLogs installs a JSON slog handler writing into buf as the default
// logger for the duration of the test, restoring the original afterwards.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return buf
}

// findRequestLog returns the first "http request" record decoded from the
// captured JSON log lines, or fails the test if none is present.
func findRequestLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec["msg"] == "http request" {
			return rec
		}
	}
	t.Fatalf("no \"http request\" log record found in:\n%s", buf.String())
	return nil
}

// The instrumentation we need to settle the iPhone investigation: every
// request that reaches vedetta must emit a structured "http request" line
// carrying enough to identify the device and whether it was a conditional
// (cache-revalidation) fetch - method, full request URI, response status,
// User-Agent, and the If-None-Match / Cache-Control request headers.
func TestRequestLogMiddlewareLogsRequestDetails(t *testing.T) {
	buf := captureLogs(t)

	h := requestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))

	req := httptest.NewRequest(http.MethodGet, "/camera.html?name=garage&fresh=1", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari")
	req.Header.Set("If-None-Match", `"abc123"`)
	req.Header.Set("Cache-Control", "max-age=0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := findRequestLog(t, buf)
	if got["method"] != http.MethodGet {
		t.Errorf("method = %v, want GET", got["method"])
	}
	if got["uri"] != "/camera.html?name=garage&fresh=1" {
		t.Errorf("uri = %v, want /camera.html?name=garage&fresh=1", got["uri"])
	}
	if got["status"] != float64(http.StatusNotModified) {
		t.Errorf("status = %v, want 304", got["status"])
	}
	if ua, _ := got["ua"].(string); ua == "" || !bytes.Contains([]byte(ua), []byte("iPhone")) {
		t.Errorf("ua = %v, want it to contain iPhone", got["ua"])
	}
	if got["if_none_match"] != `"abc123"` {
		t.Errorf("if_none_match = %v, want \"abc123\"", got["if_none_match"])
	}
	if got["cache_control"] != "max-age=0" {
		t.Errorf("cache_control = %v, want max-age=0", got["cache_control"])
	}
}

// SSE endpoints (detections, events) do w.(http.Flusher). The logging
// wrapper MUST keep that assertion working or live streams break - which
// would be a far worse regression than the bug under investigation.
func TestRequestLogMiddlewarePreservesFlusher(t *testing.T) {
	captureLogs(t)
	flushed := false

	h := requestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped ResponseWriter no longer implements http.Flusher; SSE would break")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: hi\n\n"))
		f.Flush()
		flushed = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/garage/detections", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !flushed {
		t.Fatal("handler did not reach Flush(); Flusher assertion path failed")
	}
}

// When a handler writes a body without calling WriteHeader, the logged
// status must be 200, not 0 - otherwise every streamed/implicit-200
// response (the live path) would log a misleading status.
func TestRequestLogMiddlewareDefaultsImplicit200(t *testing.T) {
	buf := captureLogs(t)

	h := requestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	got := findRequestLog(t, buf)
	if got["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200 for implicit write", got["status"])
	}
}
