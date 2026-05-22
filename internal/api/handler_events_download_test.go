package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// A maliciously crafted event label (or camera name embedded in the ID) must
// not break out of the quoted Content-Disposition filename. The download
// filename has to be sanitized to a single safe filename component.
func TestEventSnapshotDownloadFilenameSanitized(t *testing.T) {
	srv, db := newTestServer(t)

	dir := t.TempDir()
	snap := filepath.Join(dir, "snap.jpg")
	if err := os.WriteFile(snap, []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	ev := camera.Event{
		ID:                "evt1",
		CameraName:        "cam",
		Label:             `person"; attr="x`,
		Score:             0.9,
		Box:               [4]int{1, 2, 3, 4},
		Timestamp:         time.Now(),
		SnapshotPath:      snap,
		SnapshotAvailable: true,
	}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatalf("save event: %v", err)
	}

	dl := "1"
	req := httptest.NewRequest(http.MethodGet, "/api/events/evt1/snapshot?download=1", nil)
	w := httptest.NewRecorder()
	srv.GetEventSnapshot(w, req, "evt1", GetEventSnapshotParams{Download: &dl})

	cd := w.Header().Get("Content-Disposition")
	if cd == "" {
		t.Fatal("missing Content-Disposition header")
	}
	if n := strings.Count(cd, `"`); n != 2 {
		t.Fatalf("Content-Disposition has %d quotes, want 2 (filename escaped out of quoting): %q", n, cd)
	}
	if strings.Contains(cd, "; attr=") {
		t.Fatalf("Content-Disposition allows header injection: %q", cd)
	}
}

func TestEventClipDownloadFilenameSanitized(t *testing.T) {
	srv, db := newTestServer(t)

	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("fakemp4"), 0o600); err != nil {
		t.Fatalf("write clip: %v", err)
	}

	ev := camera.Event{
		ID:            "evt2",
		CameraName:    "cam",
		Label:         `car"; evil="1`,
		Score:         0.8,
		Box:           [4]int{1, 2, 3, 4},
		Timestamp:     time.Now(),
		ClipPath:      clip,
		ClipAvailable: true,
	}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatalf("save event: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events/evt2/clip?download=1", nil)
	w := httptest.NewRecorder()
	srv.GetEventClip(w, req, "evt2")

	cd := w.Header().Get("Content-Disposition")
	if cd == "" {
		t.Fatal("missing Content-Disposition header")
	}
	if n := strings.Count(cd, `"`); n != 2 {
		t.Fatalf("Content-Disposition has %d quotes, want 2: %q", n, cd)
	}
	if strings.Contains(cd, "; evil=") {
		t.Fatalf("Content-Disposition allows header injection: %q", cd)
	}
}
