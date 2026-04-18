package snapshot

import (
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSaverHappyPath(t *testing.T) {
	primary := t.TempDir()
	fallback := t.TempDir()
	s := NewSaver(primary, fallback, 85)

	img := testFrame(64, 64)
	primaryPath := filepath.Join(primary, "cam1", "evt1.jpg")

	got, err := s.Save(img, primaryPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != primaryPath {
		t.Errorf("expected primary path %q, got %q", primaryPath, got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected file to exist at %q: %v", got, err)
	}
}

func TestSaverFallsBackOnENOSPC(t *testing.T) {
	primary := t.TempDir()
	fallback := t.TempDir()
	s := NewSaver(primary, fallback, 85)

	// Inject a writer that simulates a full disk on the primary.
	s.writeFunc = func(img *image.RGBA, path string, quality int) error {
		if strings.HasPrefix(path, primary) {
			return fmt.Errorf("open %s: %w", path, &os.PathError{
				Op:   "create",
				Path: path,
				Err:  syscall.ENOSPC,
			})
		}
		return SaveSnapshot(img, path, quality)
	}

	img := testFrame(64, 64)
	primaryPath := filepath.Join(primary, "cam1", "evt1.jpg")

	got, err := s.Save(img, primaryPath)
	if err != nil {
		t.Fatalf("unexpected error on fallback: %v", err)
	}

	// Returned path must be inside the fallback root.
	if !strings.HasPrefix(got, fallback) {
		t.Errorf("expected fallback path under %q, got %q", fallback, got)
	}

	// File must exist and be a valid JPEG.
	f, err := os.Open(got)
	if err != nil {
		t.Fatalf("expected file to exist at %q: %v", got, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := jpeg.Decode(f); err != nil {
		t.Fatalf("saved fallback file is not valid JPEG: %v", err)
	}
}

func TestSaverFallbackMirrorsRelativePath(t *testing.T) {
	primary := t.TempDir()
	fallback := t.TempDir()
	s := NewSaver(primary, fallback, 85)

	enospc := &os.PathError{Op: "create", Path: "", Err: syscall.ENOSPC}
	s.writeFunc = func(img *image.RGBA, path string, quality int) error {
		if strings.HasPrefix(path, primary) {
			return fmt.Errorf("write: %w", enospc)
		}
		return SaveSnapshot(img, path, quality)
	}

	img := testFrame(32, 32)
	primaryPath := filepath.Join(primary, "cam2", "evt99.jpg")

	got, err := s.Save(img, primaryPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mirror path: fallback/cam2/evt99.jpg
	expected := filepath.Join(fallback, "cam2", "evt99.jpg")
	if got != expected {
		t.Errorf("expected mirrored fallback path %q, got %q", expected, got)
	}
}

func TestSaverErrorWhenBothFail(t *testing.T) {
	primary := t.TempDir()
	fallback := t.TempDir()
	s := NewSaver(primary, fallback, 85)

	enospc := &os.PathError{Op: "create", Path: "", Err: syscall.ENOSPC}
	s.writeFunc = func(img *image.RGBA, path string, quality int) error {
		// Both primary and fallback fail with ENOSPC.
		return fmt.Errorf("write: %w", enospc)
	}

	img := testFrame(32, 32)
	primaryPath := filepath.Join(primary, "cam1", "evt1.jpg")

	_, err := s.Save(img, primaryPath)
	if err == nil {
		t.Fatal("expected error when both primary and fallback fail")
	}
}

func TestSaverENOSPCDetection(t *testing.T) {
	// Verify that errors.Is correctly unwraps a PathError containing ENOSPC.
	pathErr := &os.PathError{Op: "create", Path: "/some/path", Err: syscall.ENOSPC}
	wrapped := fmt.Errorf("outer: %w", pathErr)
	if !errors.Is(wrapped, syscall.ENOSPC) {
		t.Error("expected errors.Is to find ENOSPC through wrapped PathError")
	}
}
