package auth

import (
	"testing"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// The dummy hash compared for unknown usernames must use the same bcrypt cost
// as the real user hashes. A cheaper dummy (e.g. MinCost) makes the unknown-user
// path measurably faster than the known-user path, leaking whether a username
// exists via a timing oracle.

func newCheckerWithUserCost(t *testing.T, cost int) *Checker {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), cost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := New(config.AuthConfig{
		Users: []config.AuthUser{{Username: "admin", PasswordHash: string(hash)}},
	}, config.APIConfig{}, db)
	t.Cleanup(c.Close)
	return c
}

func TestDummyHashMatchesUserCostDefault(t *testing.T) {
	c := newCheckerWithUserCost(t, bcrypt.DefaultCost)
	got, err := bcrypt.Cost(c.dummyHash)
	if err != nil {
		t.Fatalf("bcrypt.Cost(dummyHash): %v", err)
	}
	if got != bcrypt.DefaultCost {
		t.Fatalf("dummy hash cost = %d, want %d (matches user hash cost)", got, bcrypt.DefaultCost)
	}
}

func TestDummyHashMatchesUserCostNonDefault(t *testing.T) {
	const userCost = bcrypt.DefaultCost + 2
	c := newCheckerWithUserCost(t, userCost)
	got, err := bcrypt.Cost(c.dummyHash)
	if err != nil {
		t.Fatalf("bcrypt.Cost(dummyHash): %v", err)
	}
	if got != userCost {
		t.Fatalf("dummy hash cost = %d, want %d (matches user hash cost)", got, userCost)
	}
}
