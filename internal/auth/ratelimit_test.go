package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"

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

// Per-account scoping must not remove the aggregate per-IP cap. An attacker
// who varies the username on every attempt would otherwise never fill any
// single (IP, username) bucket, leaving the IP free to run unbounded bcrypt
// work and grow the failure map without limit. The per-IP aggregate counter
// must still throttle a username-spraying flood from one address.
func TestLoginRateLimitCapsAggregatePerIP(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	for i := range maxIPFailures {
		user := fmt.Sprintf("spray-%d", i)
		if _, err := c.Login(user, "wrong", ip, "agent", false); err != ErrInvalidCredentials {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i, err)
		}
	}
	// The IP has hit the aggregate cap; further attempts are throttled even
	// with a fresh username or valid credentials.
	if _, err := c.Login("alice", "secret", ip, "agent", false); err != ErrRateLimited {
		t.Fatalf("IP should be rate limited after %d aggregate failures, got %v", maxIPFailures, err)
	}
}

// The aggregate per-IP cap must hold under a concurrent flood, not just for
// sequential attempts. If the limit check and the counter increment are not
// atomic with respect to the bcrypt verify, a parallel burst can all pass the
// pre-check before any failure is recorded, run bcrypt en masse, and blow past
// the cap. The number of attempts that get past the reservation (i.e. actually
// run the credential check) must be bounded by maxIPFailures.
func TestLoginAggregateCapHoldsUnderConcurrency(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"
	const attempts = 200

	var (
		mu          sync.Mutex
		gotPastCap  int
		rateLimited int
		wg          sync.WaitGroup
	)
	for i := range attempts {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Distinct usernames so no per-account bucket ever fills; only the
			// aggregate per-IP cap can throttle this flood.
			_, err := c.Login(fmt.Sprintf("spray-%d", n), "wrong", ip, "agent", false)
			mu.Lock()
			defer mu.Unlock()
			switch err {
			case ErrInvalidCredentials:
				gotPastCap++
			case ErrRateLimited:
				rateLimited++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if gotPastCap > maxIPFailures {
		t.Fatalf("%d attempts ran the credential check, exceeding the per-IP cap of %d", gotPastCap, maxIPFailures)
	}
	if rateLimited == 0 {
		t.Fatalf("expected some attempts to be rate limited under a flood of %d, got none", attempts)
	}
}

// A success releases the aggregate per-IP slot it reserved, but only within the
// window it reserved against. If the window rolled over while the slow bcrypt
// verify ran (the old record expired and a fresh one was created for new
// attempts), the release must not decrement the fresh window's counter, which
// would undercount the cap and weaken the throttle at window boundaries.
func TestReleaseDoesNotDecrementNewerWindow(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	// A fresh window record exists (3 attempts), created after this success had
	// reserved against an older, now-expired window.
	fresh := time.Now()
	c.mu.Lock()
	c.ipFailures[ip] = &failureRecord{count: 3, firstAt: fresh}
	c.mu.Unlock()

	staleReservation := fresh.Add(-2 * failureWindow)
	c.completeLoginSuccess(ip, "alice", staleReservation)

	c.mu.Lock()
	got := c.ipFailures[ip]
	c.mu.Unlock()
	if got == nil || got.count != 3 {
		t.Fatalf("release against a stale window must not touch the fresh record; got %+v", got)
	}
}

// Within the same window, a success releases exactly the slot it reserved.
func TestReleaseDecrementsMatchingWindow(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	now := time.Now()
	c.mu.Lock()
	c.ipFailures[ip] = &failureRecord{count: 2, firstAt: now}
	c.mu.Unlock()

	c.completeLoginSuccess(ip, "alice", now)

	c.mu.Lock()
	got := c.ipFailures[ip]
	c.mu.Unlock()
	if got == nil || got.count != 1 {
		t.Fatalf("matching-window release should decrement to 1; got %+v", got)
	}
}

// Check backs RTSP Basic Auth, which re-authenticates on every DESCRIBE/SETUP.
// Successful checks must not consume the aggregate per-IP attempt cap, or many
// legitimate RTSP sessions behind one NAT/proxy would lock the whole IP out
// after maxIPFailures successful auths. Only failed and in-flight attempts may
// hold an aggregate slot; a success releases it.
func TestCheckSuccessDoesNotExhaustIPCap(t *testing.T) {
	c := newTwoUserChecker(t)
	const ip = "10.0.0.1"

	for i := range maxIPFailures + 10 {
		if !c.Check("alice", "secret", ip) {
			t.Fatalf("successful check %d was rate limited; valid auth must not fill the IP cap", i)
		}
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
