package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rvben/vedetta/internal/auth"
)

// changePasswordReq fires a ChangePassword request as the given principal from
// the given client address and returns the HTTP status code.
func changePasswordReq(t *testing.T, srv *Server, remoteAddr, current, next string) int {
	t.Helper()
	body := `{"current_password":"` + current + `","new_password":"` + next + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	req = requestWithPrincipal(req, &auth.Principal{Username: "admin", Kind: auth.AuthKindSession})
	w := httptest.NewRecorder()
	srv.ChangePassword(w, req)
	return w.Code
}

// TestChangePasswordRateLimitIsPerClientIP guards against the change-password
// verification being keyed on an empty client IP. A wrong-password spree from
// one IP must not exhaust a shared bucket that locks out password changes from
// a different IP.
func TestChangePasswordRateLimitIsPerClientIP(t *testing.T) {
	srv, _ := newTestServerAuth(t) // user admin / "secret"

	// Exhaust the rate-limit window from IP A with wrong current passwords.
	for i := 0; i < 10; i++ {
		if code := changePasswordReq(t, srv, "10.0.0.1:1111", "wrong-password", "newpassword123"); code != http.StatusForbidden {
			t.Fatalf("attempt %d from IP A: status = %d, want 403", i, code)
		}
	}

	// A different IP with the correct current password must still succeed:
	// its rate-limit bucket is independent of IP A's failures.
	if code := changePasswordReq(t, srv, "10.0.0.2:2222", "secret", "newpassword123"); code != http.StatusOK {
		t.Fatalf("IP B with correct password: status = %d, want 200 (change-password rate limit leaked across IPs)", code)
	}
}
