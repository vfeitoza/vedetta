package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/storage"
	"github.com/rvben/vedetta/internal/update"
	"golang.org/x/crypto/bcrypt"
)

// requestWithPrincipal returns a copy of req with the given principal injected into its context.
func requestWithPrincipal(req *http.Request, p *auth.Principal) *http.Request {
	ctx := context.WithValue(req.Context(), principalContextKey{}, p)
	return req.WithContext(ctx)
}

func TestGetMQTTSettings(t *testing.T) {
	srv, _ := newTestServer(t)

	srv.SetMQTTConfig(config.MQTTConfig{
		Enabled:  true,
		Host:     "10.0.0.1",
		Port:     1883,
		Username: "user",
		Topic:    "vedetta",
	})
	srv.mqttEnabled = true

	req := httptest.NewRequest(http.MethodGet, "/api/settings/mqtt", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", body["enabled"])
	}
	if body["host"] != "10.0.0.1" {
		t.Errorf("expected host=10.0.0.1, got %v", body["host"])
	}
	if body["status"] != "disconnected" {
		t.Errorf("expected status=disconnected, got %v", body["status"])
	}
	if _, ok := body["password"]; ok {
		t.Error("password should not be returned in GET response")
	}
}

func TestGetMQTTSettings_Disabled(t *testing.T) {
	srv, _ := newTestServer(t)

	// Default: mqttEnabled=false, mqttClient=nil
	req := httptest.NewRequest(http.MethodGet, "/api/settings/mqtt", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["status"] != "disabled" {
		t.Errorf("expected status=disabled, got %v", body["status"])
	}
}

func TestUpdateMQTTSettings(t *testing.T) {
	srv, _ := newTestServer(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	initial := "auth:\n  users:\n    - username: admin\n      password_hash: \"$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu\"\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	srv.SetConfigPath(cfgPath)

	payload := `{"enabled":true,"host":"10.0.0.5","port":1883,"username":"test","password":"secret","topic":"vedetta"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/mqtt", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["host"] != "10.0.0.5" {
		t.Errorf("expected host=10.0.0.5, got %v", body["host"])
	}
	if _, ok := body["password"]; ok {
		t.Error("password should not be returned in response")
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("10.0.0.5")) {
		t.Error("config file should contain the new host")
	}
}

// The MQTT broker password is write-only: GET never returns it, so the UI
// submits a blank password unless the operator typed a new one. A blank
// password on save must therefore keep the stored secret, not wipe it.
func TestUpdateMQTTSettings_PreservesPasswordWhenBlank(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	initial := "auth:\n  users:\n    - username: admin\n      password_hash: \"$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu\"\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	srv.SetConfigPath(cfgPath)
	srv.SetMQTTConfig(config.MQTTConfig{
		Enabled: true, Host: "10.0.0.1", Port: 1883, Username: "u", Password: "stored-secret", Topic: "vedetta",
	})

	// UI saves with a blank password (it never received the stored one).
	payload := `{"enabled":false,"host":"10.0.0.9","port":1883,"username":"u","password":"","topic":"vedetta"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/mqtt", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if srv.mqttConfig.Password != "stored-secret" {
		t.Fatalf("blank password must preserve stored secret, got %q", srv.mqttConfig.Password)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("stored-secret")) {
		t.Error("config file should retain the stored password after a blank-password save")
	}
}

// Both the save and the test paths treat a blank submitted secret as "keep
// the stored one"; a non-blank value overrides it.
func TestResolveWriteOnlySecret(t *testing.T) {
	tests := []struct {
		name      string
		submitted string
		stored    string
		want      string
	}{
		{"blank keeps stored", "", "stored", "stored"},
		{"submitted overrides", "typed", "stored", "typed"},
		{"blank with no stored stays blank", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveWriteOnlySecret(tt.submitted, tt.stored); got != tt.want {
				t.Fatalf("resolveWriteOnlySecret(%q, %q) = %q, want %q",
					tt.submitted, tt.stored, got, tt.want)
			}
		})
	}
}

// A non-blank password replaces the stored secret.
func TestUpdateMQTTSettings_SetsNewPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	initial := "auth:\n  users:\n    - username: admin\n      password_hash: \"$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu\"\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	srv.SetConfigPath(cfgPath)
	srv.SetMQTTConfig(config.MQTTConfig{
		Enabled: true, Host: "10.0.0.1", Port: 1883, Username: "u", Password: "old-secret", Topic: "vedetta",
	})

	payload := `{"enabled":false,"host":"10.0.0.1","port":1883,"username":"u","password":"new-secret","topic":"vedetta"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/mqtt", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if srv.mqttConfig.Password != "new-secret" {
		t.Fatalf("expected new password to be applied, got %q", srv.mqttConfig.Password)
	}
}

// GET must expose whether a password is stored (so the UI can show that a
// secret exists) without ever returning the secret itself.
func TestGetMQTTSettings_ReportsHasPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetMQTTConfig(config.MQTTConfig{
		Enabled: true, Host: "10.0.0.1", Port: 1883, Username: "u", Password: "secret", Topic: "vedetta",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/settings/mqtt", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["password"]; ok {
		t.Error("password must not be returned")
	}
	if body["has_password"] != true {
		t.Errorf("expected has_password=true, got %v", body["has_password"])
	}
}

func TestUpdateMQTTSettings_InvalidPort(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetConfigPath("/dev/null")

	tests := []struct {
		name    string
		payload string
	}{
		{"zero port", `{"enabled":true,"host":"localhost","port":0,"topic":"vedetta"}`},
		{"negative port", `{"enabled":true,"host":"localhost","port":-1,"topic":"vedetta"}`},
		{"port too large", `{"enabled":true,"host":"localhost","port":65536,"topic":"vedetta"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/settings/mqtt", bytes.NewBufferString(tt.payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.mux.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for invalid port, got %d", w.Code)
			}

			var body map[string]string
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body["error"] == "" {
				t.Error("expected error message in response")
			}
		})
	}
}

func TestUpdateMQTTSettings_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/mqtt", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestGetUpdateStatus_NoChecker(t *testing.T) {
	srv, _ := newTestServer(t)
	// updateChecker is nil by default

	req := httptest.NewRequest(http.MethodGet, "/api/updates/status", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["current"] != "test" {
		t.Errorf("expected current=test, got %v", body["current"])
	}
	if body["update_available"] != false {
		t.Errorf("expected update_available=false, got %v", body["update_available"])
	}
}

func TestCheckForUpdates_NoChecker(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/updates/check", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["current"] != "test" {
		t.Errorf("expected current=test, got %v", body["current"])
	}
	if body["update_available"] != false {
		t.Errorf("expected update_available=false, got %v", body["update_available"])
	}
}

func TestDismissUpdate_NoChecker(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/updates/dismiss", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestCheckForUpdates_WithChecker(t *testing.T) {
	srv, db := newTestServer(t)

	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v1.0.0","html_url":"https://github.com/rvben/vedetta/releases/tag/v1.0.0"}`))
	}))
	defer mockGH.Close()

	origURL := update.GithubLatestURL
	update.GithubLatestURL = mockGH.URL
	defer func() { update.GithubLatestURL = origURL }()

	checker := update.New("v0.1.0", 24*time.Hour, db)
	srv.SetUpdateChecker(checker)

	req := httptest.NewRequest(http.MethodGet, "/api/updates/check", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body update.Status
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !body.UpdateAvailable {
		t.Errorf("expected update_available=true, got false")
	}
	if body.Latest != "v1.0.0" {
		t.Errorf("expected latest=v1.0.0, got %v", body.Latest)
	}
}

func TestGetUpdateStatus_WithChecker(t *testing.T) {
	srv, db := newTestServer(t)

	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v2.0.0","html_url":"https://github.com/rvben/vedetta/releases/tag/v2.0.0"}`))
	}))
	defer mockGH.Close()

	origURL := update.GithubLatestURL
	update.GithubLatestURL = mockGH.URL
	defer func() { update.GithubLatestURL = origURL }()

	checker := update.New("v1.0.0", 24*time.Hour, db)
	checker.CheckNow()
	srv.SetUpdateChecker(checker)

	req := httptest.NewRequest(http.MethodGet, "/api/updates/status", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body update.Status
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !body.UpdateAvailable {
		t.Errorf("expected update_available=true, got false")
	}
	if body.Current != "v1.0.0" {
		t.Errorf("expected current=v1.0.0, got %v", body.Current)
	}
}

func TestDismissUpdate_WithChecker(t *testing.T) {
	srv, db := newTestServer(t)

	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v1.1.0","html_url":"https://github.com/rvben/vedetta/releases/tag/v1.1.0"}`))
	}))
	defer mockGH.Close()

	origURL := update.GithubLatestURL
	update.GithubLatestURL = mockGH.URL
	defer func() { update.GithubLatestURL = origURL }()

	checker := update.New("v1.0.0", 24*time.Hour, db)
	checker.CheckNow()
	srv.SetUpdateChecker(checker)

	req := httptest.NewRequest(http.MethodPost, "/api/updates/dismiss", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Status should now show dismissed=true
	status := checker.Status()
	if !status.Dismissed {
		t.Error("expected update to be dismissed after POST /api/updates/dismiss")
	}
}

func TestDiscoverMQTTBrokers(t *testing.T) {
	// mDNS discovery requires network access and uses zeroconf which may panic
	// in CI environments. Skip unless running integration tests explicitly.
	if testing.Short() {
		t.Skip("skipping mDNS discovery test in short mode")
	}
	t.Skip("skipping mDNS discovery: zeroconf double-close in test environments")
}

func TestGetRecordingSettings(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetRecordingConfig(config.RecordingConfig{
		Continuous:    true,
		RetainDays:    7,
		EventRetain:   30,
		SegmentLength: 10 * time.Minute,
		PreCapture:    5 * time.Second,
		PostCapture:   10 * time.Second,
		MaxStorage:    "500GB",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/settings/recording", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["continuous"] != true {
		t.Errorf("expected continuous=true, got %v", body["continuous"])
	}
	if body["retain_days"] != float64(7) {
		t.Errorf("expected retain_days=7, got %v", body["retain_days"])
	}
	if body["segment_length"] != "10m0s" {
		t.Errorf("expected segment_length=10m0s, got %v", body["segment_length"])
	}
}

func TestUpdateRecordingSettings(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	os.WriteFile(cfgPath, []byte("auth:\n  users:\n    - username: admin\n      password_hash: \"$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu\"\nrecording:\n  path: ./recordings\n  continuous: true\n  retain_days: 7\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"), 0644)
	srv.SetConfigPath(cfgPath)
	srv.SetRecordingConfig(config.RecordingConfig{
		Path: "./recordings", Continuous: true, RetainDays: 7,
		SegmentLength: 10 * time.Minute, PreCapture: 5 * time.Second, PostCapture: 10 * time.Second,
	})

	payload := `{"continuous":false,"retain_days":14,"event_retain_days":60,"segment_length":"5m","pre_capture":"3s","post_capture":"8s","max_storage":"1TB"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/recording", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["continuous"] != false {
		t.Errorf("expected continuous=false")
	}
	if body["retain_days"] != float64(14) {
		t.Errorf("expected retain_days=14, got %v", body["retain_days"])
	}
}

func TestUpdateRecordingSettings_InvalidDuration(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetConfigPath("/dev/null")
	srv.SetRecordingConfig(config.RecordingConfig{})

	payload := `{"continuous":true,"retain_days":7,"event_retain_days":30,"segment_length":"notaduration","pre_capture":"5s","post_capture":"10s"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/recording", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetDetectSettings_NilDetector(t *testing.T) {
	srv, _ := newTestServer(t)
	// detector is nil by default

	req := httptest.NewRequest(http.MethodGet, "/api/settings/detect", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestUpdateDetectSettings(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	os.WriteFile(cfgPath, []byte("auth:\n  users:\n    - username: admin\n      password_hash: \"$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu\"\ndetect:\n  score_threshold: 0.5\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"), 0644)
	srv.SetConfigPath(cfgPath)

	d := detect.New(config.DetectConfig{ScoreThreshold: 0.5, Labels: []string{"person"}})
	srv.SetDetector(d)

	payload := `{"score_threshold":0.75,"labels":["person","car"]}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/detect", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify hot-reload happened
	if d.ScoreThreshold() != 0.75 {
		t.Errorf("expected hot-reloaded threshold 0.75, got %v", d.ScoreThreshold())
	}
}

func TestUpdateDetectSettings_InvalidThreshold(t *testing.T) {
	srv, _ := newTestServer(t)
	d := detect.New(config.DetectConfig{ScoreThreshold: 0.5})
	srv.SetDetector(d)

	payload := `{"score_threshold":1.5,"labels":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/detect", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetAuthInfo_LocalUser(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/info", nil)
	req = requestWithPrincipal(req, &auth.Principal{
		Username: "admin",
		Kind:     "session",
	})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["username"] != "admin" {
		t.Errorf("expected username=admin, got %v", body["username"])
	}
	if body["auth_method"] != "session" {
		t.Errorf("expected auth_method=session, got %v", body["auth_method"])
	}
	if body["proxy_auth_enabled"] != false {
		t.Errorf("expected proxy_auth_enabled=false, got %v", body["proxy_auth_enabled"])
	}
}

func TestGetAuthInfo_Unauthenticated(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/info", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestChangePassword_Success(t *testing.T) {
	srv, db := newTestServer(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("oldpass1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := db.SaveAuthUser("admin", string(hash)); err != nil {
		t.Fatalf("save auth user: %v", err)
	}

	checker := auth.New(config.AuthConfig{
		Users: []config.AuthUser{{Username: "admin", PasswordHash: string(hash)}},
	}, config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()
	srv.auth = checker

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfgContent := "auth:\n  users:\n    - username: admin\n      password_hash: \"" + string(hash) + "\"\napi:\n  host: 0.0.0.0\n  port: 5050\n  exposure: lan\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	srv.SetConfigPath(cfgPath)

	payload := `{"current_password":"oldpass1","new_password":"newpass99"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithPrincipal(req, &auth.Principal{Username: "admin", Kind: "session"})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !checker.Check("admin", "newpass99", "") {
		t.Error("new password should work after change")
	}
	if checker.Check("admin", "oldpass1", "") {
		t.Error("old password should not work after change")
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	srv, db := newTestServer(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("correct1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := db.SaveAuthUser("admin", string(hash)); err != nil {
		t.Fatalf("save auth user: %v", err)
	}

	checker := auth.New(config.AuthConfig{
		Users: []config.AuthUser{{Username: "admin", PasswordHash: string(hash)}},
	}, config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()
	srv.auth = checker

	payload := `{"current_password":"wrongpass","new_password":"newpass99"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithPrincipal(req, &auth.Principal{Username: "admin", Kind: "session"})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestChangePassword_EmptyNewPassword(t *testing.T) {
	srv, _ := newTestServer(t)

	payload := `{"current_password":"oldpass1","new_password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithPrincipal(req, &auth.Principal{Username: "admin", Kind: "session"})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChangePassword_ShortNewPassword(t *testing.T) {
	srv, _ := newTestServer(t)

	payload := `{"current_password":"oldpass1","new_password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithPrincipal(req, &auth.Principal{Username: "admin", Kind: "session"})
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChangePassword_Unauthenticated(t *testing.T) {
	srv, _ := newTestServer(t)

	payload := `{"current_password":"oldpass1","new_password":"newpass99"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGetDetectSettings_WithDetector(t *testing.T) {
	srv, _ := newTestServer(t)
	d := detect.New(config.DetectConfig{ScoreThreshold: 0.65, Labels: []string{"person", "car"}})
	srv.SetDetector(d)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/detect", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	threshold, ok := body["score_threshold"].(float64)
	if !ok || threshold < 0.64 || threshold > 0.66 {
		t.Errorf("expected score_threshold ~0.65, got %v", body["score_threshold"])
	}
	labels, ok := body["labels"].([]any)
	if !ok || len(labels) != 2 {
		t.Errorf("expected 2 labels, got %v", body["labels"])
	}
}

func TestTestMQTTConnection_BadHost(t *testing.T) {
	srv, _ := newTestServer(t)

	// 192.0.2.0/24 is TEST-NET reserved by RFC 5737 — guaranteed unreachable.
	payload := `{"host":"192.0.2.1","port":1883,"username":"","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/mqtt/test", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("expected status=error for unreachable host, got %v", body["status"])
	}
}

// Ensure storage.DB satisfies the settingsStore interface used by the update checker.
// This is a compile-time check that the test helpers wire correctly.
var _ interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
	DeleteSetting(key string) error
} = (*storage.DB)(nil)
