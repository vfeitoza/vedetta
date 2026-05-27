package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/storage"
)

// stubObjectEmbedder satisfies the api.objectEmbedder interface without
// loading the real OSNet model. The recorded box/objectID let tests assert
// the handler forwarded the correct pixel-space box to the embedder.
type stubObjectEmbedder struct {
	mu       sync.Mutex
	embedded [4]int
	embedRes []float32
	embedErr error
	cropDir  string
	cropID   int64
	cropOut  string
}

func (s *stubObjectEmbedder) Embed(_ *image.RGBA, box [4]int) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedded = box
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	if s.embedRes != nil {
		return s.embedRes, nil
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (s *stubObjectEmbedder) SaveCrop(_ *image.RGBA, _ [4]int, dir string, id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cropDir = dir
	s.cropID = id
	return s.cropOut
}

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
		name           string
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

func TestCreateObjectFromCameraTrack_HappyPath(t *testing.T) {
	srv, db := newTestServer(t)

	// Register a camera with a synthetic 320x240 frame so LiveFrame returns
	// a non-nil image without RTSP wiring.
	cam := camera.NewTestCamera("front")
	frame := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			frame.Set(x, y, color.RGBA{R: 100, G: 150, B: 200, A: 255})
		}
	}
	cam.SetTestFrame(frame)
	srv.cameras.RegisterForTest(cam)

	// Stub embedder so the handler can complete the embed/crop steps.
	stub := &stubObjectEmbedder{
		embedRes: []float32{0.5, 0.4, 0.3, 0.2, 0.1},
		cropOut:  "/tmp/test-crop.jpg",
	}
	srv.objectEmbedder = stub
	// Block the rematch goroutine so the test stays deterministic.
	srv.objectRematchFn = func(int64) {}

	body, _ := json.Marshal(map[string]any{
		"track_id": 7,
		"label":    "car",
		"name":     "Renault Trafic",
		"x1":       0.10, "y1": 0.20, "x2": 0.50, "y2": 0.80,
	})
	w := postCreateObjectFromCameraTrack(t, srv, "front", string(body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var obj storage.KnownObject
	if err := json.Unmarshal(w.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if obj.ID == 0 {
		t.Errorf("expected non-zero object id, got 0")
	}
	if obj.Name != "Renault Trafic" {
		t.Errorf("expected name=Renault Trafic, got %q", obj.Name)
	}
	if obj.Label != "car" {
		t.Errorf("expected label=car, got %q", obj.Label)
	}
	if obj.CropPath != "/tmp/test-crop.jpg" {
		t.Errorf("expected crop path forwarded from stub, got %q", obj.CropPath)
	}

	// Pixel-space box: 320*0.10..320*0.50, 240*0.20..240*0.80 = (32,48,160,192).
	if stub.embedded != [4]int{32, 48, 160, 192} {
		t.Errorf("expected embed box {32,48,160,192}, got %v", stub.embedded)
	}
	if stub.cropID != obj.ID {
		t.Errorf("expected SaveCrop objectID=%d, got %d", obj.ID, stub.cropID)
	}

	// DB should now hold the object plus a reference row.
	saved, err := db.GetKnownObject(obj.ID)
	if err != nil || saved == nil {
		t.Fatalf("expected saved object, got %v err=%v", saved, err)
	}
	if saved.CropPath != "/tmp/test-crop.jpg" {
		t.Errorf("expected DB crop path persisted, got %q", saved.CropPath)
	}
}

func TestCreateObjectFromCameraTrack_EmbedderUnavailable(t *testing.T) {
	srv, _ := newTestServer(t)
	cam := camera.NewTestCamera("front")
	srv.cameras.RegisterForTest(cam)

	body, _ := json.Marshal(map[string]any{
		"track_id": 7,
		"label":    "car",
		"name":     "Truck",
		"x1":       0.1, "y1": 0.1, "x2": 0.5, "y2": 0.5,
	})
	w := postCreateObjectFromCameraTrack(t, srv, "front", string(body))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateObjectFromCameraTrack_NoLiveFrame(t *testing.T) {
	srv, _ := newTestServer(t)
	cam := camera.NewTestCamera("front") // no frame set
	srv.cameras.RegisterForTest(cam)
	srv.objectEmbedder = &stubObjectEmbedder{}

	body, _ := json.Marshal(map[string]any{
		"track_id": 7,
		"label":    "car",
		"name":     "Truck",
		"x1":       0.1, "y1": 0.1, "x2": 0.5, "y2": 0.5,
	})
	w := postCreateObjectFromCameraTrack(t, srv, "front", string(body))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no live frame") {
		t.Errorf("expected no-live-frame error, got %s", w.Body.String())
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
