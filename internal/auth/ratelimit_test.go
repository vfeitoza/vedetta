package auth

import (
	"testing"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func newTwoUserChecker(t *testing.T) *Checker {
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
		Users: []config.AuthUser{
			{Username: "alice", PasswordHash: string(hash)},
			{Username: "bob", PasswordHash: string(hash)},
		},
	}, config.APIConfig{Exposure: "lan"}, db)
	t.Cleanup(c.Close)
	return c
}

// A successful login to one account must not reset the brute-force rate-limit
// counter protecting a different account from the same IP. Keying the counter
// on the client IP alone lets an attacker who holds any valid account clear the
// limit at will: log in to their own account, then resume guessing the victim's
// password indefinitely. The counter must be scoped per (IP, username).
func TestLoginRateLimitNotResetByOtherAccountSuccess(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	// Exhaust the limit guessing bob's password from this IP.
	for range maxFailures {
		if _, err := c.Login("bob", "wrong", ip, "agent", false); err != ErrInvalidCredentials {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	}
	if _, err := c.Login("bob", "secret", ip, "agent", false); err != ErrRateLimited {
		t.Fatalf("bob should be rate limited after %d failures, got %v", maxFailures, err)
	}

	// Attacker logs in to their own account (alice) from the same IP.
	if _, err := c.Login("alice", "secret", ip, "agent", false); err != nil {
		t.Fatalf("alice login should succeed: %v", err)
	}

	// bob must still be rate limited: alice's success may not clear bob's bucket.
	if _, err := c.Login("bob", "secret", ip, "agent", false); err != ErrRateLimited {
		t.Fatalf("bob should remain rate limited after alice's login, got %v", err)
	}
}

// A successful login to an account clears that account's own failure bucket so
// a legitimate user who mistyped their password a few times can still recover.
func TestLoginSuccessClearsOwnAccountRateLimit(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	for range maxFailures - 1 {
		if _, err := c.Login("bob", "wrong", ip, "agent", false); err != ErrInvalidCredentials {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	}
	// A correct login succeeds (one attempt left) and resets bob's counter.
	if _, err := c.Login("bob", "secret", ip, "agent", false); err != nil {
		t.Fatalf("bob login should succeed: %v", err)
	}
	// After the reset, a fresh wrong attempt must not be immediately limited.
	if _, err := c.Login("bob", "wrong", ip, "agent", false); err != ErrInvalidCredentials {
		t.Fatalf("bob bucket should have been cleared, got %v", err)
	}
}

// The same per-account scoping applies to Check (RTSP basic auth): a successful
// check for one user must not clear another user's failure bucket on that IP.
func TestCheckRateLimitNotResetByOtherAccountSuccess(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	for range maxFailures {
		if c.Check("bob", "wrong", ip) {
			t.Fatal("wrong password should fail")
		}
	}
	if c.Check("bob", "secret", ip) {
		t.Fatal("bob should be rate limited")
	}
	if !c.Check("alice", "secret", ip) {
		t.Fatal("alice should authenticate")
	}
	if c.Check("bob", "secret", ip) {
		t.Fatal("bob should remain rate limited after alice's successful check")
	}
}
