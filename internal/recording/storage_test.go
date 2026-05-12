package recording

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rvben/vedetta/internal/config"
)

// newTestRecConfig builds a minimal recording config pointing at tmp.
func newTestRecConfig(tmp string) config.RecordingConfig {
	return config.RecordingConfig{
		Path:       filepath.Join(tmp, "recordings"),
		RetainDays: 7,
	}
}

func TestStorageBreakdown_SameFilesystemDetection(t *testing.T) {
	tmp := t.TempDir()
	cfg := newTestRecConfig(tmp)
	// ensure both roots exist for statfs
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Recorder{
		config:       cfg,
		snapshotPath: tmp, // same FS as cfg.Path (both under tmp)
		cameraURLs:   map[string]string{},
	}
	out, err := r.StorageBreakdown()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Snapshots.SameFilesystem {
		t.Errorf("expected same_filesystem_as_recording=true when both roots share tmp")
	}
	if out.Recording.Root == "" || out.Snapshots.Root == "" {
		t.Errorf("roots should be set: %+v %+v", out.Recording, out.Snapshots)
	}
}
