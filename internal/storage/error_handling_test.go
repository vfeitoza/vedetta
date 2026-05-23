package storage

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSavePushSubscription_ConcurrentSameEndpoint reproduces the SELECT-then-
// INSERT TOCTOU: several goroutines registering the same new endpoint at once
// all read "no existing row" before any INSERT commits, so a loser's INSERT
// hits the UNIQUE(endpoint) constraint and fails. The window is narrow, so each
// round is repeated against a fresh endpoint to make the race near-certain to
// surface before the fix; the atomic upsert lets every caller succeed, return
// the same id, and leave exactly one row per endpoint.
func TestSavePushSubscription_ConcurrentSameEndpoint(t *testing.T) {
	db := newTestDB(t)

	const (
		rounds  = 80
		workers = 32
	)
	for r := 0; r < rounds; r++ {
		endpoint := fmt.Sprintf("https://push.example/shared-%d", r)
		var wg sync.WaitGroup
		start := make(chan struct{})
		ids := make([]int64, workers)
		errs := make([]error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start // align goroutines on the check-then-insert window
				ids[idx], errs[idx] = db.SavePushSubscription(PushSubscription{
					Username: "alice",
					Endpoint: endpoint,
					P256dh:   "k",
					Auth:     "a",
				})
			}(i)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d worker %d: SavePushSubscription returned %v (TOCTOU on UNIQUE endpoint?)", r, i, err)
			}
		}
		// Every successful save must reference the single row's id.
		first := ids[0]
		for i, id := range ids {
			if id != first {
				t.Fatalf("round %d worker %d returned id %d, want shared id %d", r, i, id, first)
			}
		}
	}

	list, err := db.ListPushSubscriptionsByUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != rounds {
		t.Fatalf("expected exactly %d subscription rows (one per endpoint), got %d", rounds, len(list))
	}
}

// TestLatestObjectNameForZone_ReturnsError verifies the query no longer swallows
// failures: a real query error must surface, while a clean "no matching event"
// is reported as an empty name with a nil error (not conflated with a failure).
func TestLatestObjectNameForZone_ReturnsError(t *testing.T) {
	db := newTestDB(t)

	// No matching event yet: empty result, no error.
	name, err := db.LatestObjectNameForZone("front_yard", "person")
	if err != nil {
		t.Fatalf("no-match should not error, got %v", err)
	}
	if name != "" {
		t.Errorf("expected empty name for no match, got %q", name)
	}

	// Insert a matching event and confirm the name is returned.
	e := makeEvent("e1", "cam1", "person", 0.9, time.Now())
	e.ZoneName = "front_yard"
	e.ObjectName = "alice_car"
	mustSaveEvent(t, db, e)

	name, err = db.LatestObjectNameForZone("front_yard", "person")
	if err != nil {
		t.Fatalf("LatestObjectNameForZone: %v", err)
	}
	if name != "alice_car" {
		t.Errorf("expected alice_car, got %q", name)
	}

	// A closed DB makes the query fail; the error must surface, not be swallowed.
	_ = db.Close()
	if _, err := db.LatestObjectNameForZone("front_yard", "person"); err == nil {
		t.Error("expected an error querying a closed DB, got nil")
	}
}

// TestListAPITokensByUser_SurfacesCorruptScopes verifies a token row whose
// scopes column holds invalid JSON makes the listing fail loudly rather than
// silently returning a token with empty scopes (which would understate the
// token's privileges in the UI).
func TestListAPITokensByUser_SurfacesCorruptScopes(t *testing.T) {
	db := newTestDB(t)

	// Insert a token row directly with malformed scopes JSON.
	if _, err := db.db.Exec(`
		INSERT INTO api_tokens (username, name, token_prefix, token_hash, scopes, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"admin", "tok", "abcd", []byte("hash"), "{not valid json", utc(time.Now()),
	); err != nil {
		t.Fatal(err)
	}

	_, err := db.ListAPITokensByUser("admin")
	if err == nil {
		t.Fatal("expected an error for corrupt scopes JSON, got nil")
	}
	if !strings.Contains(err.Error(), "scopes") {
		t.Errorf("error should mention scopes, got %v", err)
	}
}
