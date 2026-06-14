package watchdog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// WriteHeapProfile writes a runtime heap profile into dir and returns the file
// path. It is the diagnostic the memory guard captures before restarting: a
// runaway leak trips the guard while it is still holding the memory, so a heap
// profile taken at that moment pins the exact allocation sites retaining it.
//
// It runs a GC first so the profile reflects live (retained) memory rather than
// not-yet-collected garbage - the restart is imminent, so the pause is
// irrelevant. nowUnix is taken as an argument (not read from the clock) so the
// filename is deterministic and the function is testable. An empty dir falls
// back to the OS temp dir, so the diagnostic is never lost just because no log
// directory is configured.
func WriteHeapProfile(dir string, nowUnix int64) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("vedetta-heap-%d.pprof", nowUnix))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create heap profile: %w", err)
	}
	defer f.Close()

	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		return "", fmt.Errorf("write heap profile: %w", err)
	}
	return path, nil
}

// ResolveHeapProfileDir chooses where trip-time heap profiles are written. It
// prefers the configured log file's directory, so the profile sits next to the
// logs. When no log file is configured (a deployment that ships logs to a
// collector instead of a file), it falls back to the database's directory:
// persistent operational storage with a stable, discoverable home. It returns
// "" only when neither path is known, leaving WriteHeapProfile to use the OS
// temp dir - a last resort, since the OS can clean a temp dir before the profile
// is analyzed.
func ResolveHeapProfileDir(logFile, dbPath string) string {
	if logFile != "" {
		return filepath.Dir(logFile)
	}
	if dbPath != "" {
		return filepath.Dir(dbPath)
	}
	return ""
}
