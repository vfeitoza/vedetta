package api

import (
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// makeFrame returns a 4x4 RGBA image with a deterministic non-uniform pattern
// so a successful JPEG encode produces a non-trivial body.
func makeFrame() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 128, A: 255})
		}
	}
	return img
}

// snapshotTestServer wires up a Server with a registered fake camera in a
// controllable state. Returns server, camera and the JPEG body of the seeded
// frame so tests can assert content-equality.
func snapshotTestServer(t *testing.T, name string, online bool, withFrame bool, frameTime time.Time) (*Server, *camera.Camera) {
	t.Helper()
	srv, _ := newTestServer(t)
	cam := camera.NewTestCamera(name)
	if withFrame {
		cam.SetTestFrame(makeFrame())
	}
	cam.SetTestOnline(online)
	cam.SetTestLastFrameTime(frameTime)
	srv.cameras.RegisterForTest(cam)
	return srv, cam
}

func TestGetCameraSnapshot_OnlineWithFrame_SetsNoCacheHeaders(t *testing.T) {
	ts := time.Date(2026, 5, 11, 14, 30, 0, 0, time.UTC)
	srv, _ := snapshotTestServer(t, "front", true, true, ts)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/front/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	if got := w.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", got)
	}

	cc := w.Header().Get("Cache-Control")
	for _, needle := range []string{"no-store", "no-cache", "must-revalidate"} {
		if !strings.Contains(cc, needle) {
			t.Errorf("Cache-Control = %q, missing %q", cc, needle)
		}
	}
	if got := w.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", got)
	}
	if got := w.Header().Get("Expires"); got != "0" {
		t.Errorf("Expires = %q, want 0", got)
	}

	wantLM := ts.UTC().Format(http.TimeFormat)
	if got := w.Header().Get("Last-Modified"); got != wantLM {
		t.Errorf("Last-Modified = %q, want %q", got, wantLM)
	}

	if w.Body.Len() == 0 {
		t.Error("response body is empty; expected JPEG bytes")
	}
}

func TestGetCameraSnapshot_OnlineWithoutFrame_Returns503(t *testing.T) {
	srv, _ := snapshotTestServer(t, "front", true, false, time.Time{})

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/front/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Vedetta-Camera-State"); got != "warming-up" {
		t.Errorf("X-Vedetta-Camera-State = %q, want warming-up", got)
	}
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want to contain no-store", cc)
	}
}

func TestGetCameraSnapshot_Offline_Returns503WithOfflineState(t *testing.T) {
	ts := time.Date(2026, 5, 11, 14, 30, 0, 0, time.UTC)
	srv, _ := snapshotTestServer(t, "front", false, true, ts)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/front/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Vedetta-Camera-State"); got != "offline" {
		t.Errorf("X-Vedetta-Camera-State = %q, want offline", got)
	}
	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want to contain no-store", cc)
	}
}

func TestGetCameraSnapshot_UnknownCamera_Returns404(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/nope/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}
