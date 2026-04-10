package media

import (
	"os"
	"testing"
)

// TestTranscodeRealSegment is a developer-only smoke test that runs the
// transcoder against a real fMP4 segment to catch memory safety issues
// the unit tests don't exercise. Set VEDETTA_REAL_SEGMENT to a segment
// path produced by Vedetta in production.
func TestTranscodeRealSegment(t *testing.T) {
	path := os.Getenv("VEDETTA_REAL_SEGMENT")
	if path == "" {
		t.Skip("set VEDETTA_REAL_SEGMENT to a real segment path")
	}
	// Copy to temp so we don't mutate the original
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp("", "vedetta_transcode_*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(src); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	if !ensureOpenH264() {
		t.Skip("OpenH264 not available")
	}

	result, err := TranscodeSegment(tmp.Name(), 1280, 720)
	if err != nil {
		t.Fatalf("transcode failed: %v", err)
	}
	if result.Skipped {
		t.Skipf("transcode skipped: source already small enough")
	}
	if result.NewSize == 0 {
		t.Errorf("output size is zero")
	}
	if result.NewSize >= result.OriginalSize {
		t.Logf("warning: new size %d not smaller than original %d", result.NewSize, result.OriginalSize)
	}
	t.Logf("OK: %d bytes -> %d bytes (%.0f%% reduction)",
		result.OriginalSize, result.NewSize,
		100*(1-float64(result.NewSize)/float64(result.OriginalSize)))
}
