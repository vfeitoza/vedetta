package camera

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// LastSnapshotTime is the timestamp the UI uses to show "last seen" for a
// camera that is no longer live. A live decode time always wins over a cached
// (disk) snapshot time.
func TestLastSnapshotTimePrefersLastFrameTime(t *testing.T) {
	cam := NewTestCamera("front")
	frameTime := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	cached := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	cam.mu.Lock()
	cam.lastFrameTime = frameTime
	cam.cachedSnapshotTime = cached
	cam.mu.Unlock()

	if got := cam.LastSnapshotTime(); !got.Equal(frameTime) {
		t.Fatalf("LastSnapshotTime() = %v, want live lastFrameTime %v", got, frameTime)
	}
}

// After a restart there is no live frame yet, but a cached snapshot loaded
// from disk still gives the UI a "last seen" time.
func TestLastSnapshotTimeFallsBackToCachedSnapshotTime(t *testing.T) {
	cam := NewTestCamera("front")
	cached := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	cam.mu.Lock()
	cam.cachedSnapshotTime = cached // lastFrameTime stays zero
	cam.mu.Unlock()

	if got := cam.LastSnapshotTime(); !got.Equal(cached) {
		t.Fatalf("LastSnapshotTime() = %v, want cachedSnapshotTime %v", got, cached)
	}
}

func TestLastSnapshotTimeZeroWhenNoFrameEverSeen(t *testing.T) {
	cam := NewTestCamera("front")
	if got := cam.LastSnapshotTime(); !got.IsZero() {
		t.Fatalf("LastSnapshotTime() = %v, want zero for a camera that never produced a frame", got)
	}
}

// loadCachedSnapshot must both make the cached frame available for serving and
// stamp its age from the file's mtime, without making the camera look online.
func TestLoadCachedSnapshotSetsCachedSnapshotTimeFromMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.jpg")
	writeTestJPEG(t, path)

	mtime := time.Date(2026, 5, 30, 8, 15, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cam := NewTestCamera("front")
	cam.latestSnapshotPath = path

	cam.loadCachedSnapshot()

	if cam.LastSnapshot() == nil {
		t.Fatal("LastSnapshot() = nil after loadCachedSnapshot; cached frame not loaded")
	}
	if got := cam.LastSnapshotTime(); !got.Equal(mtime) {
		t.Fatalf("LastSnapshotTime() = %v, want file mtime %v", got, mtime)
	}
	if cam.IsOnline() {
		t.Fatal("IsOnline() = true after loading a cached snapshot; a stale disk frame must not count as live")
	}
}

func writeTestJPEG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 90, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode: %v", err)
	}
}
