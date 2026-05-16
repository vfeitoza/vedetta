package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/recording"
	"github.com/rvben/vedetta/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

type contractValidator struct {
	router routers.Router
	t      *testing.T
}

func newContractValidator(t *testing.T) *contractValidator {
	t.Helper()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("validate openapi spec: %v", err)
	}

	// Required for path matching — the router needs a server URL to strip prefixes
	spec.Servers = openapi3.Servers{{URL: "/"}}

	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		t.Fatalf("create openapi router: %v", err)
	}

	return &contractValidator{router: router, t: t}
}

func (cv *contractValidator) validate(req *http.Request, rec *httptest.ResponseRecorder) {
	cv.t.Helper()

	route, pathParams, err := cv.router.FindRoute(req)
	if err != nil {
		cv.t.Fatalf("find route for %s %s: %v", req.Method, req.URL.Path, err)
	}

	reqInput := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			// Skip auth validation in contract tests
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
	}

	body := rec.Body.Bytes()
	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(body)),
	}

	if err := openapi3filter.ValidateResponse(context.Background(), respInput); err != nil {
		cv.t.Errorf("response validation failed for %s %s (status %d):\n%s\nBody: %s",
			req.Method, req.URL.Path, rec.Code, err, string(body))
	}
}

func TestContract_GetHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetHealthLive(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health/live", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetHealthReady(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health/ready", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	// Ready endpoint may return 200 or 503 depending on state
	cv.validate(req, rec)
}

func TestContract_GetSystem(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetStreamingCapabilities(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	disabled := false
	srv.cameraConfigs = []config.CameraConfig{
		{Name: "front_door", URL: "rtsp://cam/sub", RecordURL: "rtsp://cam/main"},
		{Name: "old_cam", URL: "rtsp://cam/x", Enabled: &disabled},
	}
	srv.SetRTSPServerConfig(config.RTSPServerConfig{Enabled: true, Port: 8554})

	req := httptest.NewRequest(http.MethodGet, "/api/streaming/capabilities", nil)
	req.Host = "vedetta.lan:5050"
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp StreamingCapabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.AuthRequired {
		t.Errorf("auth_required = true, want false (newTestServer has no auth)")
	}
	if !resp.RtspServer.Enabled || resp.RtspServer.Port != 8554 {
		t.Errorf("rtsp_server = %+v, want {enabled:true port:8554}", resp.RtspServer)
	}
	if len(resp.Cameras) != 1 {
		t.Fatalf("expected 1 enabled camera (disabled excluded), got %d: %+v",
			len(resp.Cameras), resp.Cameras)
	}

	cam := resp.Cameras[0]
	if cam.Name != "front_door" {
		t.Fatalf("camera name = %q, want front_door", cam.Name)
	}
	if cam.Streams.RtspMain == nil || *cam.Streams.RtspMain != "rtsp://vedetta.lan:8554/front_door" {
		t.Errorf("rtsp_main = %v", cam.Streams.RtspMain)
	}
	if cam.Streams.RtspSub == nil || *cam.Streams.RtspSub != "rtsp://vedetta.lan:8554/front_door_sub" {
		t.Errorf("rtsp_sub = %v", cam.Streams.RtspSub)
	}
	if cam.Streams.Webrtc == nil || *cam.Streams.Webrtc != "/api/cameras/front_door/webrtc/offer" {
		t.Errorf("webrtc = %v", cam.Streams.Webrtc)
	}
}

// newTestServerWithAuth creates a Server with auth enabled, a mux, and routes registered.
// It returns the server, its handler (mux wrapped in auth middleware), and the auth checker.
func newTestServerWithAuth(t *testing.T) (*Server, http.Handler, *auth.Checker) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	apiCfg := config.APIConfig{Exposure: "lan", Host: "127.0.0.1", Port: 0}
	checker := auth.New(config.AuthConfig{
		Users: []config.AuthUser{{
			Username:     "admin",
			PasswordHash: string(hash),
		}},
	}, apiCfg, db)
	t.Cleanup(checker.Close)

	rec := recording.New(config.RecordingConfig{
		Path: t.TempDir(),
	}, config.EventConfig{RetainDays: 90}, nil, db, nil, "", "", nil)

	srv := New(apiCfg, checker, db)
	srv.SetSubsystems(nil, rec, nil, nil, nil, "", "", nil, nil)

	handler := authMiddleware(srv, srv.mux)
	return srv, handler, checker
}

func TestContract_Login(t *testing.T) {
	_, handler, _ := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	body := `{"username":"admin","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_Login_InvalidCredentials(t *testing.T) {
	_, handler, _ := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	body := `{"username":"admin","password":"wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_AuthMe(t *testing.T) {
	_, handler, checker := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	// Login to get a session
	session, err := checker.Login("admin", "secret", "10.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Get session cookies
	loginRec := httptest.NewRecorder()
	checker.SetSessionCookies(loginRec, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	// Verify the response contains expected fields
	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["username"] != "admin" {
		t.Errorf("username = %v, want admin", resp["username"])
	}
	if resp["kind"] != "session" {
		t.Errorf("kind = %v, want session", resp["kind"])
	}
}

func TestContract_Logout(t *testing.T) {
	_, handler, checker := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	session, err := checker.Login("admin", "secret", "10.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	loginRec := httptest.NewRecorder()
	checker.SetSessionCookies(loginRec, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_CreateToken(t *testing.T) {
	_, handler, checker := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	session, err := checker.Login("admin", "secret", "10.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	loginRec := httptest.NewRecorder()
	checker.SetSessionCookies(loginRec, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	body := `{"name":"test-token","scopes":["read:cameras"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_DeleteToken(t *testing.T) {
	_, handler, checker := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	session, err := checker.Login("admin", "secret", "10.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Create a token first
	token, _, err := checker.CreateToken("admin", "to-delete", nil, "10.0.0.1")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	loginRec := httptest.NewRecorder()
	checker.SetSessionCookies(loginRec, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/tokens/%d", token.ID), nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_ListTokens(t *testing.T) {
	_, handler, checker := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	session, err := checker.Login("admin", "secret", "10.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Create a token so the list is non-empty
	_, _, err = checker.CreateToken("admin", "my-token", []string{"read:cameras"}, "10.0.0.1")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	loginRec := httptest.NewRecorder()
	checker.SetSessionCookies(loginRec, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	// Verify envelope structure
	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["items"]; !ok {
		t.Error("response missing 'items' field")
	}
	if _, ok := resp["total"]; !ok {
		t.Error("response missing 'total' field")
	}
	items, ok := resp["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 token, got %v", resp["items"])
	}
	token := items[0].(map[string]any)
	if token["name"] != "my-token" {
		t.Errorf("name = %v, want my-token", token["name"])
	}
	if _, ok := token["token_prefix"]; !ok {
		t.Error("token missing 'token_prefix' field")
	}
}

func TestContract_ListTokens_Empty(t *testing.T) {
	_, handler, checker := newTestServerWithAuth(t)
	cv := newContractValidator(t)

	session, err := checker.Login("admin", "secret", "10.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	loginRec := httptest.NewRecorder()
	checker.SetSessionCookies(loginRec, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["total"] != float64(0) {
		t.Errorf("expected total=0, got %v", resp["total"])
	}
}

func TestContract_ListCameras(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	// Verify envelope structure
	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["items"]; !ok {
		t.Error("response missing 'items' field")
	}
	if _, ok := resp["total"]; !ok {
		t.Error("response missing 'total' field")
	}
	if _, ok := resp["has_more"]; !ok {
		t.Error("response missing 'has_more' field")
	}
}

func TestContract_ListCameras_WithCamera(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	// Add a camera so the list is non-empty
	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})

	req := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := resp["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item, got %v", resp["items"])
	}
	cam := items[0].(map[string]any)
	if cam["name"] != "test_cam" {
		t.Errorf("name = %v, want test_cam", cam["name"])
	}
}

func TestContract_GetCamera(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/test_cam", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetCamera_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_ListZones(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/test_cam/zones", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_ListEvents(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["items"]; !ok {
		t.Error("response missing 'items' field")
	}
	if _, ok := resp["total"]; !ok {
		t.Error("response missing 'total' field")
	}
	if _, ok := resp["has_more"]; !ok {
		t.Error("response missing 'has_more' field")
	}
}

func TestContract_ListEvents_WithData(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "contract-evt-1", "cam1", "person", 0.9, now)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetEvent(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "contract-evt-2", "cam1", "person", 0.85, now)

	req := httptest.NewRequest(http.MethodGet, "/api/events/contract-evt-2", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetEvent_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/events/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetEventCounts(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "contract-cnt-1", "cam1", "person", 0.9, now)

	req := httptest.NewRequest(http.MethodGet, "/api/events/counts", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_ListSegments(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})

	now := time.Now().UTC().Truncate(time.Second)
	seedSegment(t, db, "test_cam", "/tmp/seg-c1.mp4", now.Add(-10*time.Minute), now, 1048576)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/recordings/segments/test_cam?date=%s", now.Format("2006-01-02")), nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["items"]; !ok {
		t.Error("response missing 'items' field")
	}
	if _, ok := resp["total"]; !ok {
		t.Error("response missing 'total' field")
	}
	if _, ok := resp["has_more"]; !ok {
		t.Error("response missing 'has_more' field")
	}
	items := resp["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(items))
	}
	seg := items[0].(map[string]any)
	if _, ok := seg["id"]; !ok {
		t.Error("segment missing 'id' field")
	}
}

func TestContract_RecordingsCalendar(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	day := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	seedSegment(t, db, "cam1", "/tmp/cal-c1.mp4", day, day.Add(time.Hour), 1024)

	req := httptest.NewRequest(http.MethodGet, "/api/recordings/calendar?month=2025-03", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	days, ok := resp["days"].([]any)
	if !ok {
		t.Fatal("response missing 'days' array")
	}
	if len(days) != 1 {
		t.Fatalf("expected 1 day, got %d", len(days))
	}
}

func TestContract_RecordingsSummary(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	seedSegment(t, db, "cam1", "/tmp/sum-c1.mp4", now, now.Add(10*time.Minute), 1024*1024)

	req := httptest.NewRequest(http.MethodGet, "/api/recordings/summary?date=2025-03-15", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["cameras"]; !ok {
		t.Error("response missing 'cameras' field")
	}
	if _, ok := resp["total_bytes"]; !ok {
		t.Error("response missing 'total_bytes' field")
	}
}

func TestContract_ListPeople(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/people", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	// Verify envelope response
	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatal("expected 'items' array in envelope")
	}
	if len(items) != 0 {
		t.Errorf("expected empty items array, got %d items", len(items))
	}
	if resp["total"] != float64(0) {
		t.Errorf("expected total=0, got %v", resp["total"])
	}
}

func TestContract_ListUnmatchedFaces(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/faces/unmatched", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_ListObjects(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/objects", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	// Verify envelope response
	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatal("expected 'items' array in envelope")
	}
	if len(items) != 0 {
		t.Errorf("expected empty items array, got %d items", len(items))
	}
	if resp["total"] != float64(0) {
		t.Errorf("expected total=0, got %v", resp["total"])
	}
}

func TestContract_StopCamera(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	// Add a camera and put it in running state by calling StartCamera on the manager.
	// The camera goroutine exits immediately because the hub is nil.
	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})
	if err := srv.cameras.StartCamera(context.Background(), "test_cam"); err != nil {
		t.Fatalf("StartCamera: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/test_cam/stop", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	// Verify response contains stopped=true and camera name.
	var status map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status["name"] != "test_cam" {
		t.Errorf("name = %v, want test_cam", status["name"])
	}
	if status["stopped"] != true {
		t.Errorf("stopped = %v, want true", status["stopped"])
	}
}

func TestContract_StopCamera_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/nonexistent/stop", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_StopCamera_AlreadyStopped(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	// Add camera, start it (goroutine exits immediately with nil hub), then stop once.
	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})
	if err := srv.cameras.StartCamera(context.Background(), "test_cam"); err != nil {
		t.Fatalf("StartCamera: %v", err)
	}

	// First stop succeeds.
	req1 := httptest.NewRequest(http.MethodPost, "/api/cameras/test_cam/stop", nil)
	rec1 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first stop: expected 200, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Second stop must return 409 Conflict.
	req2 := httptest.NewRequest(http.MethodPost, "/api/cameras/test_cam/stop", nil)
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
	cv.validate(req2, rec2)
}

func TestContract_StartCamera_AfterStop(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	// Add camera, start it, stop it via the API, then start it again via the API.
	// Each cam.Start call spawns a goroutine that exits immediately with a nil hub.
	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})
	if err := srv.cameras.StartCamera(context.Background(), "test_cam"); err != nil {
		t.Fatalf("StartCamera: %v", err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/cameras/test_cam/stop", nil)
	stopRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("stop: expected 200, got %d: %s", stopRec.Code, stopRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/test_cam/start", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var status map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status["name"] != "test_cam" {
		t.Errorf("name = %v, want test_cam", status["name"])
	}
	if status["stopped"] != false {
		t.Errorf("stopped = %v, want false", status["stopped"])
	}
}

func TestContract_StartCamera_AlreadyRunning(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	// Add camera and start it (goroutine exits immediately with nil hub).
	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})
	if err := srv.cameras.StartCamera(context.Background(), "test_cam"); err != nil {
		t.Fatalf("StartCamera: %v", err)
	}

	// Calling start on an already-running camera must return 409 Conflict.
	req := httptest.NewRequest(http.MethodPost, "/api/cameras/test_cam/start", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_StartCamera_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodPost, "/api/cameras/nonexistent/start", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_CameraTimeline(t *testing.T) {
	srv, db := newTestServer(t)
	cv := newContractValidator(t)

	srv.cameras.AddCamera(config.CameraConfig{Name: "test_cam", URL: "rtsp://localhost/stream"})

	now := time.Now().UTC().Truncate(time.Second)
	seedSegment(t, db, "test_cam", "/tmp/tl-c1.mp4", now.Add(-10*time.Minute), now, 1048576)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/cameras/test_cam/timeline?date=%s", now.Format("2006-01-02")), nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)

	var resp map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["segments"]; !ok {
		t.Error("response missing 'segments' field")
	}
	if _, ok := resp["events"]; !ok {
		t.Error("response missing 'events' field")
	}
	if _, ok := resp["activity"]; !ok {
		t.Error("response missing 'activity' field")
	}
}
