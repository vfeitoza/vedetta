package api

import (
	"regexp"
	"strings"
	"testing"
)

// The camera-grid loads its snapshots via htmx hx-trigger="load". Empirical
// testing against the vendored htmx 2.0.4 bundle shows that the initial
// declarative load swap fires htmx:load but does NOT deliver an
// htmx:afterSwap to a document-level listener; afterSwap only fires for
// later programmatic htmx.ajax() swaps (e.g. the camera start/stop toggle).
//
// So a grid-refresh bootstrap wired solely to htmx:afterSwap never starts
// on a normal page load: snapshots paint once from the partial's raw
// <img src> and never refresh. These tests lock the bootstrap contract.

func appJS(t *testing.T) string {
	t.Helper()
	b, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read embedded static/app.js: %v", err)
	}
	return string(b)
}

// TestGridRefreshBootstrappedOnHtmxLoad is the regression guard for the
// "snapshots load once and never update" bug: the refresh must be armed
// from htmx:load, the event that actually fires for the initial
// declarative camera-grid swap.
func TestGridRefreshBootstrappedOnHtmxLoad(t *testing.T) {
	js := appJS(t)

	if !strings.Contains(js, "ensureGridSnapshotRefresh") {
		t.Fatal("expected an idempotent ensureGridSnapshotRefresh() bootstrap; " +
			"refresh must not depend solely on a full startGridSnapshotRefresh restart")
	}

	// An htmx:load listener must exist AND invoke the grid bootstrap. Match
	// a listener registration followed (within the same handler body) by an
	// ensureGridSnapshotRefresh call.
	htmxLoadHandler := regexp.MustCompile(
		`addEventListener\(\s*['"]htmx:load['"][\s\S]{0,400}?ensureGridSnapshotRefresh\(`)
	if !htmxLoadHandler.MatchString(js) {
		t.Fatal("camera-grid refresh is not bootstrapped from htmx:load; " +
			"htmx:afterSwap does not fire for the initial hx-trigger=\"load\" grid swap, " +
			"so the 30s snapshot refresh never starts on a normal page load")
	}
}

// TestGridRefreshGuardIsIdempotent ensures the htmx:load bootstrap will not
// thrash the interval: htmx:load also fires for the system-status partial
// every 10s, so an unguarded restart on every htmx:load would clear and
// recreate the 30s timer before it ever fires, and snapshots would still
// never refresh. ensureGridSnapshotRefresh must early-return when the
// interval is already running.
func TestGridRefreshGuardIsIdempotent(t *testing.T) {
	js := appJS(t)

	fn := regexp.MustCompile(
		`function ensureGridSnapshotRefresh\(\)\s*\{[\s\S]{0,300}?gridSnapshotInterval`)
	if !fn.MatchString(js) {
		t.Fatal("ensureGridSnapshotRefresh() must check gridSnapshotInterval and " +
			"early-return when already running, or the 10s system-status htmx:load " +
			"will keep resetting the 30s grid timer")
	}
}
