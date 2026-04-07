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
	}, config.EventConfig{RetainDays: 90}, nil, db, nil, "")

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
