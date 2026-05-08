package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postCreateObjectFromCameraTrack drives the endpoint through the registered
// mux so we exercise routing + handler together.
func postCreateObjectFromCameraTrack(t *testing.T, srv *Server, cameraName, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/cameras/"+cameraName+"/objects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

func TestCreateObjectFromCameraTrack_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	w := postCreateObjectFromCameraTrack(t, srv, "any", "not json")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid JSON") {
		t.Errorf("expected invalid JSON error, got %s", w.Body.String())
	}
}

func TestCreateObjectFromCameraTrack_EmptyName(t *testing.T) {
	srv, _ := newTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"track_id": 7,
		"label":    "car",
		"name":     "   ",
		"x1":       0.1, "y1": 0.1, "x2": 0.5, "y2": 0.5,
	})
	w := postCreateObjectFromCameraTrack(t, srv, "any", string(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateObjectFromCameraTrack_MissingLabel(t *testing.T) {
	srv, _ := newTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"track_id": 7,
		"label":    "",
		"name":     "Renault Trafic",
		"x1":       0.1, "y1": 0.1, "x2": 0.5, "y2": 0.5,
	})
	w := postCreateObjectFromCameraTrack(t, srv, "any", string(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "label is required") {
		t.Errorf("expected label-required error, got %s", w.Body.String())
	}
}

func TestCreateObjectFromCameraTrack_InvalidBox(t *testing.T) {
	srv, _ := newTestServer(t)

	cases := []struct {
		name string
		x1, y1, x2, y2 float64
	}{
		{"negative_x1", -0.1, 0.1, 0.5, 0.5},
		{"y2_over_one", 0.1, 0.1, 0.5, 1.5},
		{"inverted_x", 0.5, 0.1, 0.4, 0.5},
		{"inverted_y", 0.1, 0.5, 0.5, 0.4},
		{"degenerate", 0.3, 0.3, 0.3, 0.3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"track_id": 7,
				"label":    "car",
				"name":     "Renault Trafic",
				"x1":       tc.x1, "y1": tc.y1, "x2": tc.x2, "y2": tc.y2,
			})
			w := postCreateObjectFromCameraTrack(t, srv, "any", string(body))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "invalid normalized box") {
				t.Errorf("expected invalid-box error, got %s", w.Body.String())
			}
		})
	}
}

func TestCreateObjectFromCameraTrack_CameraNotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"track_id": 7,
		"label":    "car",
		"name":     "Renault Trafic",
		"x1":       0.1, "y1": 0.1, "x2": 0.5, "y2": 0.5,
	})
	w := postCreateObjectFromCameraTrack(t, srv, "ghost_camera", string(body))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "camera not found") {
		t.Errorf("expected camera-not-found error, got %s", w.Body.String())
	}
}
