package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterRotatesAndCapsBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	// 100-byte cap, keep 2 backups.
	rw, err := NewRotatingWriter(path, 100, 2)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer rw.Close()

	line := []byte(strings.Repeat("x", 60) + "\n") // 61 bytes; two lines exceed 100
	for i := 0; i < 6; i++ {
		if _, err := rw.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// The active file and exactly two rotated backups must exist; the third
	// must have been pruned by the maxBackups cap.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("backup .1 missing: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("backup .2 missing: %v", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("backup .3 must not exist with maxBackups=2 (err=%v)", err)
	}
}

func TestRotatingWriterKeepsActiveFileUnderCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	rw, err := NewRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer rw.Close()

	for i := 0; i < 50; i++ {
		if _, err := rw.Write([]byte(strings.Repeat("y", 40) + "\n")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat active: %v", err)
	}
	// A single write (41 bytes) never exceeds the 100-byte cap, so the active
	// file must always stay below the cap plus one write.
	if info.Size() > 100 {
		t.Fatalf("active log grew past cap: %d bytes", info.Size())
	}
}
