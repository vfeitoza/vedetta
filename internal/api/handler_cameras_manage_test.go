package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rvben/vedetta/internal/config"
)

func newTestServerWithCameras(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _ := newTestServer(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	os.WriteFile(cfgPath, []byte("auth:\n  users:\n    - username: admin\n      password_hash: \"$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu\"\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"), 0644)
	srv.SetConfigPath(cfgPath)

	enabled := true
	srv.cameraConfigs = []config.CameraConfig{
		{Name: "front", URL: "rtsp://front", Enabled: &enabled},
		{Name: "back", URL: "rtsp://back", Enabled: &enabled},
	}
	for _, cam := range srv.cameraConfigs {
		config.AppendCamera(cfgPath, cam, "")
	}

	return srv, cfgPath
}

func TestListCamerasManage(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/manage", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	cameras := body["cameras"].([]any)
	if len(cameras) != 2 {
		t.Fatalf("expected 2 cameras, got %d", len(cameras))
	}
	if body["restart_required"] != false {
		t.Error("expected restart_required=false initially")
	}
}

// The management list must never ship RTSP credentials to the browser: it
// returns the credential-stripped URL plus a has_credentials flag so the UI
// can show that a secret exists without exposing it.
func TestListCamerasManage_StripsCredentials(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)
	enabled := true
	srv.cameraConfigs = []config.CameraConfig{
		{Name: "front", URL: "rtsp://admin:s3cret@front.lan/stream1", RecordURL: "rtsp://admin:s3cret@front.lan/stream0", Enabled: &enabled},
		{Name: "plain", URL: "rtsp://plain.lan/stream1", Enabled: &enabled},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/manage", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("s3cret")) {
		t.Fatalf("response leaked credentials: %s", w.Body.String())
	}

	var body struct {
		Cameras []struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			RecordURL      string `json:"record_url"`
			HasCredentials bool   `json:"has_credentials"`
		} `json:"cameras"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Cameras[0].URL != "rtsp://front.lan/stream1" {
		t.Errorf("url not stripped: %q", body.Cameras[0].URL)
	}
	if body.Cameras[0].RecordURL != "rtsp://front.lan/stream0" {
		t.Errorf("record_url not stripped: %q", body.Cameras[0].RecordURL)
	}
	if !body.Cameras[0].HasCredentials {
		t.Error("expected has_credentials=true for camera with userinfo")
	}
	if body.Cameras[1].HasCredentials {
		t.Error("expected has_credentials=false for camera without userinfo")
	}
}

// When the UI saves the credential-stripped URL unchanged, the server must
// re-attach the stored credentials so editing other fields does not silently
// wipe the camera's secret.
func TestUpdateCameraManage_PreservesStrippedCredentials(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)
	enabled := true
	srv.cameraConfigs[0] = config.CameraConfig{
		Name: "front", URL: "rtsp://admin:s3cret@front.lan/stream1", Enabled: &enabled,
	}

	// UI sends back the stripped URL (no userinfo).
	payload := `{"name":"front","url":"rtsp://front.lan/stream1","enabled":true,"detect":{"width":640,"height":480,"fps":5},"record":{"width":1920,"height":1080,"fps":15}}`
	req := httptest.NewRequest(http.MethodPut, "/api/cameras/manage/0", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if srv.cameraConfigs[0].URL != "rtsp://admin:s3cret@front.lan/stream1" {
		t.Fatalf("credentials not preserved on update: %q", srv.cameraConfigs[0].URL)
	}
}

// When the operator types a URL with fresh userinfo, the new credentials win.
func TestUpdateCameraManage_AcceptsNewCredentials(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)
	enabled := true
	srv.cameraConfigs[0] = config.CameraConfig{
		Name: "front", URL: "rtsp://admin:s3cret@front.lan/stream1", Enabled: &enabled,
	}

	payload := `{"name":"front","url":"rtsp://newuser:newpass@front.lan/stream1","enabled":true,"detect":{"width":640,"height":480,"fps":5},"record":{"width":1920,"height":1080,"fps":15}}`
	req := httptest.NewRequest(http.MethodPut, "/api/cameras/manage/0", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if srv.cameraConfigs[0].URL != "rtsp://newuser:newpass@front.lan/stream1" {
		t.Fatalf("new credentials not applied: %q", srv.cameraConfigs[0].URL)
	}
}

func TestAddCameraManage(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	payload := `{"name":"garage","url":"rtsp://garage","enabled":true,"detect":{"width":640,"height":480,"fps":5},"record":{"width":1920,"height":1080,"fps":15}}`
	req := httptest.NewRequest(http.MethodPost, "/api/cameras/manage", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(srv.cameraConfigs) != 3 {
		t.Fatalf("expected 3 cameras, got %d", len(srv.cameraConfigs))
	}
	if !srv.restartRequired {
		t.Error("expected restartRequired=true after add")
	}
}

func TestUpdateCameraManage(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	payload := `{"name":"front_updated","url":"rtsp://front-new","enabled":true,"detect":{"width":640,"height":480,"fps":5},"record":{"width":1920,"height":1080,"fps":15}}`
	req := httptest.NewRequest(http.MethodPut, "/api/cameras/manage/0", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if srv.cameraConfigs[0].Name != "front_updated" {
		t.Errorf("expected front_updated, got %s", srv.cameraConfigs[0].Name)
	}
}

func TestRemoveCameraManage(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/cameras/manage/0", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(srv.cameraConfigs) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(srv.cameraConfigs))
	}
	if srv.cameraConfigs[0].Name != "back" {
		t.Errorf("expected back, got %s", srv.cameraConfigs[0].Name)
	}
}

func TestRemoveCameraManage_InvalidIndex(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/cameras/manage/99", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAddCameraManage_InvalidJSON(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/manage", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAddCameraManage_MissingURL(t *testing.T) {
	srv, _ := newTestServerWithCameras(t)

	payload := `{"name":"test","url":"","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/cameras/manage", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
