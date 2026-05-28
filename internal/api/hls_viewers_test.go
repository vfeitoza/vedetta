package api

import (
	"testing"
	"time"
)

func TestHLSViewers_CountsActiveClients(t *testing.T) {
	v := newHLSViewers()
	now := time.Unix(1_700_000_000, 0)
	v.now = func() time.Time { return now }

	v.seen("front_door", "192.0.2.1:5001")
	v.seen("front_door", "192.0.2.1:5002")
	v.seen("garage", "198.51.100.5:33000")

	got := v.counts()
	if got["front_door"] != 2 {
		t.Fatalf("front_door: want 2, got %d", got["front_door"])
	}
	if got["garage"] != 1 {
		t.Fatalf("garage: want 1, got %d", got["garage"])
	}
}

func TestHLSViewers_ExpiresStaleClients(t *testing.T) {
	v := newHLSViewers()
	now := time.Unix(1_700_000_000, 0)
	v.now = func() time.Time { return now }

	v.seen("front_door", "192.0.2.1:5001") // stale once we advance
	now = now.Add(15 * time.Second)
	v.seen("front_door", "192.0.2.1:5002") // fresh

	// Advance past the TTL of the first client but not the second.
	now = now.Add(hlsViewerTTL - 5*time.Second)

	got := v.counts()
	if got["front_door"] != 1 {
		t.Fatalf("front_door after TTL: want 1, got %d", got["front_door"])
	}

	// Advance past TTL of both — camera should drop out of the map entirely.
	now = now.Add(hlsViewerTTL)
	got = v.counts()
	if _, ok := got["front_door"]; ok {
		t.Fatalf("front_door should be gone, got %v", got)
	}
}

func TestHLSViewers_IgnoresEmptyArgs(t *testing.T) {
	v := newHLSViewers()
	v.seen("", "192.0.2.1:5001")
	v.seen("front_door", "")
	if got := v.counts(); len(got) != 0 {
		t.Fatalf("expected no entries, got %v", got)
	}
}
