package watchdog

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteHeapProfile_WritesValidGzippedProfile proves the guard's diagnostic
// dump produces a real pprof heap profile (gzip-compressed protobuf) at a path
// under the requested directory, so a runaway that trips the guard leaves behind
// an analyzable artifact pinning what held the memory.
func TestWriteHeapProfile_WritesValidGzippedProfile(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteHeapProfile(dir, 1717000000)
	if err != nil {
		t.Fatalf("WriteHeapProfile: %v", err)
	}
	if got := filepath.Dir(path); got != dir {
		t.Fatalf("profile written to %s, want a file under %s", path, dir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading profile: %v", err)
	}
	// pprof heap profiles are gzip-compressed protobufs (magic 0x1f 0x8b).
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Fatalf("profile %s is not a gzip-compressed pprof profile", path)
	}
}

// TestWriteHeapProfile_EmptyDirFallsBackToTemp proves an unset directory does not
// fail the dump (it must never lose the diagnostic just because no log dir is
// configured); the profile lands in the OS temp dir instead.
func TestWriteHeapProfile_EmptyDirFallsBackToTemp(t *testing.T) {
	path, err := WriteHeapProfile("", 1717000001)
	if err != nil {
		t.Fatalf("WriteHeapProfile: %v", err)
	}
	defer func() { _ = os.Remove(path) }()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected profile at %s: %v", path, err)
	}
}
