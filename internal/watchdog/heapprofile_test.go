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

// TestResolveHeapProfileDir_PrefersLogDir proves the log file's directory wins
// when a log file is configured, keeping the profile next to the logs.
func TestResolveHeapProfileDir_PrefersLogDir(t *testing.T) {
	got := ResolveHeapProfileDir("/var/log/vedetta/vedetta.log", "/data/vedetta.db")
	if want := "/var/log/vedetta"; got != want {
		t.Fatalf("ResolveHeapProfileDir = %q, want %q", got, want)
	}
}

// TestResolveHeapProfileDir_FallsBackToDBDir proves that when no log file is
// configured (the deployment ships logs to a collector, not a file), the profile
// lands in the database's directory: persistent operational storage that always
// has a stable home, rather than an OS temp dir that can be cleaned before the
// profile is analyzed.
func TestResolveHeapProfileDir_FallsBackToDBDir(t *testing.T) {
	got := ResolveHeapProfileDir("", "/Users/op/vedetta/vedetta.db")
	if want := "/Users/op/vedetta"; got != want {
		t.Fatalf("ResolveHeapProfileDir = %q, want %q", got, want)
	}
}

// TestResolveHeapProfileDir_EmptyWhenNothingKnown proves that with neither a log
// file nor a database path, the resolver returns "" so WriteHeapProfile applies
// its own OS-temp-dir fallback rather than this function inventing a path.
func TestResolveHeapProfileDir_EmptyWhenNothingKnown(t *testing.T) {
	if got := ResolveHeapProfileDir("", ""); got != "" {
		t.Fatalf("ResolveHeapProfileDir = %q, want empty", got)
	}
}
