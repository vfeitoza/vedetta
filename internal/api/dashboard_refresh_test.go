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
			"will keep resetting the grid timer")
	}
}

// TestGridRefreshIsMotionAdaptive locks the bandwidth-smart cadence: the
// expensive per-camera JPEG snapshot must be pulled fast only for cameras
// whose /api/cameras status reports has_motion, and slowly for idle ones.
// A single fixed interval that pulls every online camera every tick would
// scale bandwidth linearly with camera count for no perceptual gain.
func TestGridRefreshIsMotionAdaptive(t *testing.T) {
	js := appJS(t)

	for _, id := range []string{"GRID_TICK_MS", "GRID_IDLE_MS"} {
		if !strings.Contains(js, id) {
			t.Fatalf("expected motion-adaptive cadence constant %s "+
				"(fast tick for motion cameras, slow cadence for idle ones)", id)
		}
	}

	// The driver interval must run at the fast tick, not a fixed 30000.
	tick := regexp.MustCompile(`setInterval\(\s*refreshGridSnapshots\s*,\s*GRID_TICK_MS\s*\)`)
	if !tick.MatchString(js) {
		t.Fatal("grid driver must tick at GRID_TICK_MS so motion cameras can " +
			"refresh quickly; idle cameras are gated inside the tick")
	}

	// The snapshot pull must be gated per camera on has_motion plus a
	// per-camera last-refresh timestamp, so idle cameras are skipped on
	// most ticks instead of being fetched every GRID_TICK_MS.
	gate := regexp.MustCompile(`has_motion[\s\S]{0,400}?GRID_IDLE_MS`)
	if !gate.MatchString(js) {
		t.Fatal("snapshot refresh must be gated on has_motion and GRID_IDLE_MS " +
			"per camera; idle cameras must not be pulled every tick")
	}
	if !strings.Contains(js, "gridLastSnap") {
		t.Fatal("expected a per-camera last-snapshot timestamp map " +
			"(gridLastSnap) to gate idle cameras across ticks")
	}
}

// gridFnBody returns the source of a top-level app.js function declaration,
// bounded by the next top-level `function ` declaration.
func gridFnBody(t *testing.T, js, fn string) string {
	t.Helper()
	marker := "function " + fn + "("
	start := strings.Index(js, marker)
	if start < 0 {
		t.Fatalf("function %s not found in app.js", fn)
	}
	rest := js[start+len(marker):]
	if end := strings.Index(rest, "\nfunction "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestGridSnapshotUsesRawCameraName is the regression guard for the
// "blank camera tiles after a restart" bug: the grid snapshot URL and the
// /api/cameras status lookup must use the RAW camera name from the card's
// data-camera-name attribute, never img.alt.
//
// img.alt is the display name plus " camera" (e.g. "Garage camera"), so
// deriving the snapshot name from it requests /api/cameras/Garage%20camera/
// snapshot (404) and looks up statusMap["Garage camera"] (miss), which makes
// refreshGridSnapshots return for every tile. The grid then never recovers
// after the warmup 503 that follows a restart, and every preview stays blank.
func TestGridSnapshotUsesRawCameraName(t *testing.T) {
	js := appJS(t)

	imgAlt := regexp.MustCompile(`\bimg\.alt\b`)
	for _, fn := range []string{"initGridSnapshotStates", "refreshGridSnapshots"} {
		body := gridFnBody(t, js, fn)

		if !strings.Contains(body, "data-camera-name") {
			t.Fatalf("%s must resolve the camera name from the card's "+
				"data-camera-name attribute (the raw name) so snapshot URLs and "+
				"status lookups use the real camera name", fn)
		}
		if imgAlt.MatchString(body) {
			t.Fatalf("%s must not derive the camera name from img.alt; img.alt is "+
				"the display name plus \" camera\" (e.g. \"Garage camera\"), which "+
				"404s the snapshot endpoint and misses the status map", fn)
		}
	}
}
