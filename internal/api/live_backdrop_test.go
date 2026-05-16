package api

import (
	"regexp"
	"strings"
	"testing"
)

// The camera grid already paints a working snapshot per camera. Opening a
// camera detail page must not regress to a black void while the live
// transport warms up (iOS native HLS warmup is ~1-2s+). Both the MSE path
// and the iOS startNativeHLS path must paint the latest snapshot as a
// backdrop behind the connecting overlay, via a single shared helper, so
// the user sees the same image they just tapped instead of nothing.

// TestSnapshotBackdropHelperExists locks the shared helper contract: the
// snapshot-backdrop logic must be factored into one function rather than
// duplicated per transport (so a new transport cannot silently omit it).
func TestSnapshotBackdropHelperExists(t *testing.T) {
	js := appJS(t)

	if !strings.Contains(js, "function showSnapshotBackdrop(") {
		t.Fatal("expected a shared showSnapshotBackdrop(name) helper that sets " +
			"the live-viewport snapshot backdrop; the backdrop must not be " +
			"duplicated inline per transport")
	}

	// The helper must point the viewport background at the camera snapshot
	// endpoint (cache-busted) and flag the fallback styling.
	body := regexp.MustCompile(
		`function showSnapshotBackdrop\([\s\S]{0,400}?/snapshot[\s\S]{0,200}?live-snapshot-fallback`)
	if !body.MatchString(js) {
		t.Fatal("showSnapshotBackdrop must set viewport backgroundImage to the " +
			"camera /snapshot endpoint and add the live-snapshot-fallback class")
	}
}

// TestNativeHLSShowsSnapshotBackdrop is the regression guard for the
// reported symptom: tapping a camera that shows a snapshot opens a detail
// page with no snapshot. startNativeHLS must invoke showSnapshotBackdrop
// during connect so iOS users see the snapshot, not a black void, for the
// HLS warmup window.
func TestNativeHLSShowsSnapshotBackdrop(t *testing.T) {
	js := appJS(t)

	hls := regexp.MustCompile(
		`function startNativeHLS\([\s\S]{0,900}?showSnapshotBackdrop\(`)
	if !hls.MatchString(js) {
		t.Fatal("startNativeHLS does not paint the snapshot backdrop during " +
			"connect; iOS users see a black void for the HLS warmup window " +
			"even though the grid already had a working snapshot")
	}
}

// TestMSEShowsSnapshotBackdrop guards the existing MSE behavior through the
// same shared helper, so the refactor does not drop the desktop backdrop.
func TestMSEShowsSnapshotBackdrop(t *testing.T) {
	js := appJS(t)

	mse := regexp.MustCompile(
		`function startMSE\([\s\S]{0,900}?showSnapshotBackdrop\(`)
	if !mse.MatchString(js) {
		t.Fatal("startMSE must paint the snapshot backdrop via the shared " +
			"showSnapshotBackdrop helper")
	}
}
