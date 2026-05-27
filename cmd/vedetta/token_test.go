package main

import (
	"crypto/sha256"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// newTokenTestDB opens a throwaway database seeded with the given auth users,
// mirroring the auth_users table the running server resolves token owners
// against.
func newTokenTestDB(t *testing.T, usernames ...string) *storage.DB {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, u := range usernames {
		if err := db.SeedAuthUser(u, "hash:"+u); err != nil {
			t.Fatalf("seed user %q: %v", u, err)
		}
	}
	return db
}

// resolveTokenOwner defaults to the sole account in auth_users, validates an
// explicit user against that table, and refuses to mint when the owner is
// missing or ambiguous.
func TestResolveTokenOwner(t *testing.T) {
	t.Run("sole user is the default owner", func(t *testing.T) {
		got, err := resolveTokenOwner(newTokenTestDB(t, "admin"), "")
		if err != nil || got != "admin" {
			t.Fatalf("got (%q, %v), want (admin, nil)", got, err)
		}
	})
	t.Run("explicit known user", func(t *testing.T) {
		got, err := resolveTokenOwner(newTokenTestDB(t, "admin", "ops"), "ops")
		if err != nil || got != "ops" {
			t.Fatalf("got (%q, %v), want (ops, nil)", got, err)
		}
	})
	t.Run("unknown user is rejected", func(t *testing.T) {
		if _, err := resolveTokenOwner(newTokenTestDB(t, "admin"), "ghost"); err == nil {
			t.Fatal("expected error for unknown user")
		}
	})
	t.Run("ambiguous owner requires -user", func(t *testing.T) {
		if _, err := resolveTokenOwner(newTokenTestDB(t, "admin", "ops"), ""); err == nil {
			t.Fatal("expected error when multiple users and no -user")
		}
	})
	t.Run("no users in database", func(t *testing.T) {
		if _, err := resolveTokenOwner(newTokenTestDB(t), ""); err == nil {
			t.Fatal("expected error when auth_users is empty")
		}
	})
}

// splitScopes trims whitespace and drops empty entries so a sloppy
// "-scopes a, ,b," still yields a clean scope list, and empty input yields no
// scopes rather than a single empty entry.
func TestSplitScopes(t *testing.T) {
	if got := splitScopes(" metrics:read , api:read ,, "); !slices.Equal(got, []string{"metrics:read", "api:read"}) {
		t.Errorf("splitScopes(messy) = %v, want [metrics:read api:read]", got)
	}
	if got := splitScopes(""); len(got) != 0 {
		t.Errorf("splitScopes(\"\") = %v, want []", got)
	}
}

// mintToken persists a usable token: the returned plaintext hashes to a stored
// row carrying the requested owner and scopes, so a Prometheus scraper can
// authenticate with it.
func TestMintTokenPersistsUsableToken(t *testing.T) {
	db := newTokenTestDB(t, "admin")

	raw, token, err := mintToken(db, config.AuthConfig{}, config.APIConfig{}, "", "prometheus", []string{"metrics:read"})
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	if raw == "" {
		t.Fatal("plaintext token is empty")
	}
	if token.Username != "admin" || token.Name != "prometheus" {
		t.Errorf("owner/name = %q/%q, want admin/prometheus", token.Username, token.Name)
	}
	if len(token.Scopes) != 1 || token.Scopes[0] != "metrics:read" {
		t.Errorf("scopes = %v, want [metrics:read]", token.Scopes)
	}

	// The plaintext must resolve to the stored row, proving the token a scraper
	// presents will authenticate.
	hash := sha256.Sum256([]byte(raw))
	stored, err := db.GetAPITokenByHash(hash[:])
	if err != nil {
		t.Fatalf("plaintext does not resolve to a stored token: %v", err)
	}
	if stored.ID != token.ID {
		t.Errorf("stored id %d != returned id %d", stored.ID, token.ID)
	}
}

// A user present only in the database (for example created through the UI)
// must be able to own a token even when auth.users in the YAML is empty,
// because auth_users is the runtime source of truth.
func TestMintTokenResolvesDatabaseOnlyUser(t *testing.T) {
	db := newTokenTestDB(t, "operator")

	_, token, err := mintToken(db, config.AuthConfig{}, config.APIConfig{}, "operator", "prometheus", []string{"metrics:read"})
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	if token.Username != "operator" {
		t.Errorf("owner = %q, want operator", token.Username)
	}
}

// Users declared only in the YAML config are seeded into auth_users before
// resolution, matching server startup, so an offline mint on a database that
// predates the server's first run still references a valid account.
func TestMintTokenSeedsConfigUser(t *testing.T) {
	db := newTokenTestDB(t)
	cfg := config.AuthConfig{Users: []config.AuthUser{{Username: "admin", PasswordHash: "x"}}}

	_, token, err := mintToken(db, cfg, config.APIConfig{}, "", "prometheus", []string{"metrics:read"})
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	if token.Username != "admin" {
		t.Errorf("owner = %q, want admin", token.Username)
	}
}
