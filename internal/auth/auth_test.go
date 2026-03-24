package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func newChecker(t *testing.T, apiCfg config.APIConfig) *Checker {
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

	c := New(config.AuthConfig{
		Users: []config.AuthUser{{
			Username:     "admin",
			PasswordHash: string(hash),
		}},
	}, apiCfg, db)
	t.Cleanup(c.Close)
	return c
}

func TestValidateConfigRejectsMalformedHash(t *testing.T) {
	err := ValidateConfig(config.AuthConfig{
		Users: []config.AuthUser{{
			Username:     "admin",
			PasswordHash: "not-a-bcrypt-hash",
		}},
	})
	if err == nil {
		t.Fatal("expected malformed hash validation error")
	}
}

func TestCheckRateLimitIsPerIP(t *testing.T) {
	c := newChecker(t, config.APIConfig{Exposure: "lan"})

	for range maxFailures {
		if c.Check("admin", "wrong", "10.0.0.1") {
			t.Fatal("wrong password should fail")
		}
	}
	if c.Check("admin", "secret", "10.0.0.1") {
		t.Fatal("same IP should be rate limited")
	}
	if !c.Check("admin", "secret", "10.0.0.2") {
		t.Fatal("different IP should not be rate limited")
	}
}

func TestSessionAuthenticationAndCSRF(t *testing.T) {
	c := newChecker(t, config.APIConfig{Exposure: "lan"})

	session, err := c.Login("admin", "secret", "10.0.0.1", "test-agent", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	rr := httptest.NewRecorder()
	c.SetSessionCookies(rr, httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil), session)

	req := httptest.NewRequest(http.MethodPost, "/api/cameras", nil)
	for _, cookie := range rr.Result().Cookies() {
		req.AddCookie(cookie)
	}

	principal, err := c.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if principal == nil || principal.Kind != AuthKindSession {
		t.Fatalf("expected session principal, got %+v", principal)
	}

	if c.RequireCSRF(req, principal) {
		t.Fatal("POST without X-CSRF-Token should fail")
	}
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	if !c.RequireCSRF(req, principal) {
		t.Fatal("POST with matching CSRF token should pass")
	}
}

func TestSetSessionCookies_LANHTTPDoesNotForceSecure(t *testing.T) {
	c := newChecker(t, config.APIConfig{Exposure: "lan"})

	session, err := c.Login("admin", "secret", "10.0.0.1", "test-agent", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://vedetta.local/api/auth/login", nil)
	rr := httptest.NewRecorder()
	c.SetSessionCookies(rr, req, session)

	for _, cookie := range rr.Result().Cookies() {
		if cookie.Secure {
			t.Fatalf("cookie %q unexpectedly marked Secure on plain HTTP LAN request", cookie.Name)
		}
	}
}

func TestSetSessionCookies_SecureTransportUsesSecureCookies(t *testing.T) {
	c := newChecker(t, config.APIConfig{Exposure: "lan"})

	session, err := c.Login("admin", "secret", "10.0.0.1", "test-agent", false)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://vedetta.local/api/auth/login", nil)
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()
	c.SetSessionCookies(rr, req, session)

	for _, cookie := range rr.Result().Cookies() {
		if !cookie.Secure {
			t.Fatalf("cookie %q should be Secure on HTTPS requests", cookie.Name)
		}
	}
}

func TestBearerTokenAuthentication(t *testing.T) {
	c := newChecker(t, config.APIConfig{Exposure: "lan"})

	token, rawToken, err := c.CreateToken("admin", "integration", []string{"api:read"}, "10.0.0.1")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if token.ID == 0 {
		t.Fatal("expected token ID")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	principal, err := c.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if principal == nil || principal.Kind != AuthKindToken {
		t.Fatalf("expected token principal, got %+v", principal)
	}
	if !principal.HasAnyScope("api:read") {
		t.Fatal("expected api:read scope")
	}
	if principal.Allows(http.MethodDelete, "/api/events") {
		t.Fatal("read-only token should not allow DELETE")
	}
}

func TestChecker_DBAuth(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer db.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	db.SaveAuthUser("admin", string(hash))

	checker := NewFromDB(config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()

	// Valid login
	session, err := checker.Login("admin", "secret", "127.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login should succeed: %v", err)
	}
	if session.Username != "admin" {
		t.Errorf("expected username 'admin', got %q", session.Username)
	}

	// Invalid password
	_, err = checker.Login("admin", "wrong", "127.0.0.1", "test", false)
	if err == nil {
		t.Error("Login with wrong password should fail")
	}

	// Unknown user
	_, err = checker.Login("nobody", "secret", "127.0.0.1", "test", false)
	if err == nil {
		t.Error("Login with unknown user should fail")
	}
}

func TestChangePassword(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer db.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpassword"), bcrypt.DefaultCost)
	db.SaveAuthUser("admin", string(hash))

	checker := NewFromDB(config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()

	// Change password succeeds with correct current password
	if err := checker.ChangePassword("admin", "oldpassword", "newpassword123"); err != nil {
		t.Fatalf("ChangePassword should succeed: %v", err)
	}

	// Login with new password succeeds
	session, err := checker.Login("admin", "newpassword123", "127.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login with new password should succeed: %v", err)
	}
	if session.Username != "admin" {
		t.Errorf("expected username 'admin', got %q", session.Username)
	}

	// Login with old password fails
	_, err = checker.Login("admin", "oldpassword", "127.0.0.1", "test", false)
	if err == nil {
		t.Error("Login with old password should fail after change")
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer db.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	db.SaveAuthUser("admin", string(hash))

	checker := NewFromDB(config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()

	err = checker.ChangePassword("admin", "wrongpassword", "newpassword123")
	if err == nil {
		t.Fatal("ChangePassword with wrong current password should fail")
	}
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginRememberMe(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer db.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	db.SaveAuthUser("admin", string(hash))

	checker := NewFromDB(config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()

	// Login with remember=true: 30-day expiry
	session, err := checker.Login("admin", "secret", "127.0.0.1", "test", true)
	if err != nil {
		t.Fatalf("Login with remember=true should succeed: %v", err)
	}
	expectedExpiry := 30 * 24 * time.Hour
	actualExpiry := session.ExpiresAt.Sub(session.CreatedAt)
	if actualExpiry < expectedExpiry-time.Second || actualExpiry > expectedExpiry+time.Second {
		t.Errorf("remember session expiry = %v, want ~%v", actualExpiry, expectedExpiry)
	}
	if session.IdleTTL != 7*24*time.Hour {
		t.Errorf("remember session idle TTL = %v, want %v", session.IdleTTL, 7*24*time.Hour)
	}

	// Login with remember=false: 12-hour expiry
	session2, err := checker.Login("admin", "secret", "127.0.0.1", "test", false)
	if err != nil {
		t.Fatalf("Login with remember=false should succeed: %v", err)
	}
	expectedExpiry2 := 12 * time.Hour
	actualExpiry2 := session2.ExpiresAt.Sub(session2.CreatedAt)
	if actualExpiry2 < expectedExpiry2-time.Second || actualExpiry2 > expectedExpiry2+time.Second {
		t.Errorf("standard session expiry = %v, want ~%v", actualExpiry2, expectedExpiry2)
	}
	if session2.IdleTTL != 30*time.Minute {
		t.Errorf("standard session idle TTL = %v, want %v", session2.IdleTTL, 30*time.Minute)
	}
}

func TestRememberSessionIdleTTL(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer db.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	db.SaveAuthUser("admin", string(hash))

	checker := NewFromDB(config.APIConfig{Exposure: "lan"}, db)
	defer checker.Close()

	// Create a remember session
	session, err := checker.Login("admin", "secret", "127.0.0.1", "test", true)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Verify the session can be retrieved and has correct idle TTL
	retrieved, err := db.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if retrieved.IdleTTL != 7*24*time.Hour {
		t.Errorf("stored idle TTL = %v, want %v", retrieved.IdleTTL, 7*24*time.Hour)
	}

	// Authenticate with the session via HTTP request
	rr := httptest.NewRecorder()
	checker.SetSessionCookies(rr, httptest.NewRequest(http.MethodGet, "http://vedetta.local/", nil), session)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	for _, cookie := range rr.Result().Cookies() {
		req.AddCookie(cookie)
	}
	principal, err := checker.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if principal == nil || principal.Username != "admin" {
		t.Fatalf("expected admin principal, got %+v", principal)
	}
}

func TestRequestIsSecureWithTrustedProxy(t *testing.T) {
	c := newChecker(t, config.APIConfig{
		Exposure:       "internet",
		TrustedProxies: []string{"127.0.0.1/32"},
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	if !c.RequestIsSecure(req) {
		t.Fatal("trusted proxy with X-Forwarded-Proto=https should be secure")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.9:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	if c.RequestIsSecure(req) {
		t.Fatal("untrusted proxy should not be treated as secure")
	}
}
