package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
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

// gorilla/websocket's Upgrade() does w.(http.Hijacker). The logging
// wrapper MUST forward Hijack or every WebSocket endpoint (MSE live
// video) breaks behind requestLogMiddleware - exactly the production
// outage this fixes. Served via httptest.NewServer because only a real
// server connection (not httptest.ResponseRecorder) is hijackable.
func TestRequestLogMiddlewarePreservesHijacker(t *testing.T) {
	captureLogs(t)
	var sawHijacker bool

	h := requestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHijacker = w.(http.Hijacker)
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/cameras/garage/mse/ws")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if !sawHijacker {
		t.Fatal("wrapped ResponseWriter does not implement http.Hijacker; WebSocket upgrades (MSE live video) would fail")
	}
}

// End-to-end: a real WebSocket client upgrading through the exact
// logging middleware that wraps every production response. Fails today
// (500, no upgrade); passes once Hijack() is forwarded. Also asserts
// the access log reports a truthful 101 rather than a misleading 200.
func TestRequestLogMiddlewareAllowsWebSocketUpgrade(t *testing.T) {
	buf := captureLogs(t)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	h := requestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		defer c.Close()
		mt, msg, err := c.ReadMessage()
		if err != nil {
			t.Errorf("server read failed: %v", err)
			return
		}
		_ = c.WriteMessage(mt, msg)
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/cameras/garage/mse/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed (upgrade rejected): %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101", resp.StatusCode)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("client write failed: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("client read failed: %v", err)
	}
	if string(msg) != "ping" {
		t.Fatalf("echo = %q, want \"ping\"", msg)
	}

	got := findRequestLog(t, buf)
	if got["status"] != float64(http.StatusSwitchingProtocols) {
		t.Errorf("logged status = %v, want 101 for a hijacked upgrade", got["status"])
	}
}
