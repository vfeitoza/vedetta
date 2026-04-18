package snapshot

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// Saver writes event snapshots to a primary location and transparently falls
// back to a local-disk reserve directory when the primary volume is full.
// Construct with NewSaver; the zero value is not valid.
type Saver struct {
	primaryRoot  string
	fallbackRoot string
	quality      int

	// writeFunc is the underlying writer. Replaced in tests to simulate errors.
	writeFunc func(img *image.RGBA, path string, quality int) error
}

// NewSaver returns a Saver that writes to primaryRoot and falls back to
// fallbackRoot when the primary volume reports ENOSPC.
func NewSaver(primaryRoot, fallbackRoot string, quality int) *Saver {
	return &Saver{
		primaryRoot:  primaryRoot,
		fallbackRoot: fallbackRoot,
		quality:      quality,
		writeFunc:    SaveSnapshot,
	}
}

// DefaultFallbackRoot returns the default fallback directory:
// ~/.vedetta/snapshots-fallback/ when the home directory is resolvable,
// otherwise $TMPDIR/vedetta-snapshots-fallback/.
func DefaultFallbackRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("cannot determine home directory for snapshot fallback; using temp dir",
			"error", err)
		return filepath.Join(os.TempDir(), "vedetta-snapshots-fallback")
	}
	return filepath.Join(home, ".vedetta", "snapshots-fallback")
}

// Save writes img to primaryPath. If the write fails because the primary
// volume is full (ENOSPC), it remaps the relative sub-path to fallbackRoot
// and retries there. The actual path written is returned so callers can
// persist the correct location.
func (s *Saver) Save(img *image.RGBA, primaryPath string) (string, error) {
	err := s.writeFunc(img, primaryPath, s.quality)
	if err == nil {
		return primaryPath, nil
	}

	if !errors.Is(err, syscall.ENOSPC) {
		return "", err
	}

	// Primary volume is full — fall back to the local-disk reserve.
	fallbackPath := s.fallbackPath(primaryPath)
	slog.Info("primary snapshot volume full; writing to fallback",
		"primary", primaryPath,
		"fallback", fallbackPath,
	)

	if fbErr := s.writeFunc(img, fallbackPath, s.quality); fbErr != nil {
		return "", fmt.Errorf("primary full (%w); fallback also failed: %v", err, fbErr)
	}

	return fallbackPath, nil
}

// fallbackPath computes the destination under fallbackRoot by mirroring the
// relative portion of primaryPath beneath primaryRoot. If primaryPath is not
// under primaryRoot (defensive), just the base name is used.
func (s *Saver) fallbackPath(primaryPath string) string {
	rel, err := filepath.Rel(s.primaryRoot, primaryPath)
	if err != nil || rel == ".." || len(rel) > 0 && rel[0] == '.' {
		// Path escapes the primary root — use base name only.
		rel = filepath.Base(primaryPath)
	}
	return filepath.Join(s.fallbackRoot, rel)
}
