package recording

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// newTestRecConfig builds a minimal recording config pointing at tmp.
func newTestRecConfig(tmp string) config.RecordingConfig {
	return config.RecordingConfig{
		Path:       filepath.Join(tmp, "recordings"),
		RetainDays: 7,
	}
}

func openTestStorageDB(t *testing.T) *storage.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeDummySegment(t *testing.T, root, cam string, when time.Time, size int64) string {
	t.Helper()
	dir := filepath.Join(root, cam, "segments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := when.UTC().Format("2006-01-02T15-04-05") + ".mp4"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustSaveSegmentRecord(t *testing.T, db *storage.DB, cam, path string, start time.Time, size int64) {
	t.Helper()
	rec := storage.SegmentRecord{
		Camera:    cam,
		Path:      path,
		StartTime: start,
		EndTime:   start.Add(10 * time.Second),
		SizeBytes: size,
	}
	if err := db.SaveSegment(rec); err != nil {
		t.Fatal(err)
	}
}

func mustInsertEvent(t *testing.T, db *storage.DB, cam string, start, end time.Time, clipPath, snapshotPath string) {
	t.Helper()
	ev := camera.Event{
		ID:                fmt.Sprintf("ev-%d", time.Now().UnixNano()),
		CameraName:        cam,
		Timestamp:         start,
		EndTime:           end,
		ClipPath:          clipPath,
		ClipAvailable:     clipPath != "",
		SnapshotPath:      snapshotPath,
		SnapshotAvailable: snapshotPath != "",
	}
	if err := db.SaveEvent(ev); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteStorage_Clips_NoWindow(t *testing.T) {
	tmp := t.TempDir()
	db := openTestStorageDB(t)
	clipFile := filepath.Join(tmp, "clip-1.mp4")
	snapFile := filepath.Join(tmp, "snap-1.jpg")
	os.WriteFile(clipFile, []byte("c"), 0o644)
	os.WriteFile(snapFile, []byte("s"), 0o644)

	mustInsertEvent(t, db, "cam-a", time.Now().UTC(), time.Time{}, clipFile, snapFile)
	r := &Recorder{db: db, segments: &SegmentRecorder{db: db}, config: newTestRecConfig(tmp)}

	res, err := r.DeleteStorage(DeleteRequest{Target: DeleteClips, Camera: "cam-a"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Clips != 1 || res.Snapshots != 1 {
		t.Errorf("got %+v, want 1 clip + 1 snapshot", res)
	}
}

func TestDeleteStorage_FreeBytes_OldestFirst(t *testing.T) {
	tmp := t.TempDir()
	db := openTestStorageDB(t)
	old := time.Now().UTC().AddDate(0, 0, -10)
	mid := time.Now().UTC().AddDate(0, 0, -5)
	oldF := writeDummySegment(t, tmp, "cam-a", old, 1000)
	midF := writeDummySegment(t, tmp, "cam-a", mid, 2000)
	mustSaveSegmentRecord(t, db, "cam-a", oldF, old, 1000)
	mustSaveSegmentRecord(t, db, "cam-a", midF, mid, 2000)

	r := &Recorder{db: db, segments: &SegmentRecorder{db: db}, config: newTestRecConfig(tmp)}
	res, err := r.DeleteStorage(DeleteRequest{Target: DeleteFreeBytes, FreeBytesTarget: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes < 1500 {
		t.Errorf("freed %d, want >= 1500", res.Bytes)
	}
	if _, err := os.Stat(oldF); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("oldest should be deleted first")
	}
}

func TestDeleteStorage_Segments_OlderThanDays(t *testing.T) {
	tmp := t.TempDir()
	db := openTestStorageDB(t)
	old := time.Now().UTC().AddDate(0, 0, -10)
	recent := time.Now().UTC().AddDate(0, 0, -1)
	oldFile := writeDummySegment(t, tmp, "cam-a", old, 1024)
	recentFile := writeDummySegment(t, tmp, "cam-a", recent, 2048)
	mustSaveSegmentRecord(t, db, "cam-a", oldFile, old, 1024)
	mustSaveSegmentRecord(t, db, "cam-a", recentFile, recent, 2048)

	r := &Recorder{db: db, segments: &SegmentRecorder{}, config: newTestRecConfig(tmp)}

	result, err := r.DeleteStorage(DeleteRequest{
		Target:        DeleteSegments,
		Camera:        "cam-a",
		OlderThanDays: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments != 1 || result.Bytes != 1024 {
		t.Errorf("got %+v, want 1 segment / 1024 bytes", result)
	}
	if _, err := os.Stat(oldFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old segment file should be gone")
	}
	if _, err := os.Stat(recentFile); err != nil {
		t.Errorf("recent segment file should remain: %v", err)
	}
}

func TestDeleteStorage_DryRun_NoFilesystemChanges(t *testing.T) {
	tmp := t.TempDir()
	db := openTestStorageDB(t)
	old := time.Now().UTC().AddDate(0, 0, -10)
	oldFile := writeDummySegment(t, tmp, "cam-a", old, 1024)
	mustSaveSegmentRecord(t, db, "cam-a", oldFile, old, 1024)

	r := &Recorder{db: db, segments: &SegmentRecorder{}, config: newTestRecConfig(tmp)}
	result, err := r.DeleteStorage(DeleteRequest{
		Target: DeleteSegments, Camera: "cam-a", OlderThanDays: 7, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments != 1 || result.Bytes != 1024 {
		t.Errorf("dry-run should still report what would be deleted: %+v", result)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Errorf("dry-run must not delete the file: %v", err)
	}
}

func TestDeleteStorage_RejectsInvalidShapes(t *testing.T) {
	r := &Recorder{}
	cases := []DeleteRequest{
		{Target: DeleteSegments},                                        // missing camera + window
		{Target: DeleteSegments, Camera: "cam-a"},                       // missing window
		{Target: DeleteSegments, Camera: "cam-a", From: "2026-01-01"},   // only From, no To
		{Target: DeleteFreeBytes},                                       // missing free_bytes_target
		{Target: DeleteFreeBytes, FreeBytesTarget: 100, Camera: "cam-a"}, // extra fields
		{Target: "garbage"},
	}
	for i, req := range cases {
		if _, err := r.DeleteStorage(req); err == nil {
			t.Errorf("case %d: expected error for %+v", i, req)
		}
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

func TestDeleteStorage_All_PropagatesOpenSegmentProtection(t *testing.T) {
	tmp := t.TempDir()
	db := openTestStorageDB(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	old := today.AddDate(0, 0, -5)
	todayStr := today.Format("2006-01-02")

	// Seed an older closed segment.
	oldFile := writeDummySegment(t, tmp, "cam-a", old, 1024)
	mustSaveSegmentRecord(t, db, "cam-a", oldFile, old, 1024)

	// Seed today's open segment (the one that should be protected).
	openFile := writeDummySegment(t, tmp, "cam-a", today, 512)
	mustSaveSegmentRecord(t, db, "cam-a", openFile, today, 512)

	// Seed a clip so the deleteAllScoped clip path is exercised.
	clipFile := filepath.Join(tmp, "clip.mp4")
	os.WriteFile(clipFile, []byte("clip"), 0o644)
	mustInsertEvent(t, db, "cam-a", today, today.Add(time.Minute), clipFile, "")

	r := &Recorder{
		db:       db,
		segments: &SegmentRecorder{db: db},
		config:   newTestRecConfig(tmp),
	}
	// Register the open consumer so openFile appears in CurrentSegmentPaths.
	r.RegisterFakeOpenConsumer("cam-a", openFile)

	_, err := r.DeleteStorage(DeleteRequest{
		Target: DeleteAll,
		Camera: "cam-a",
		From:   todayStr,
		To:     todayStr,
	})

	// Must return a protection error, not nil.
	var protErr *ErrOpenSegmentProtected
	if !errors.As(err, &protErr) {
		t.Fatalf("got err=%v, want *ErrOpenSegmentProtected", err)
	}
	found := false
	for _, p := range protErr.Paths {
		if p == openFile {
			found = true
		}
	}
	if !found {
		t.Errorf("protected Paths %v does not contain open segment %s", protErr.Paths, openFile)
	}
}

func TestTryRunCleanupAsync_BusyReturnsError(t *testing.T) {
	r := &Recorder{}
	r.segmentOpMu.Lock() // simulate in-flight
	defer r.segmentOpMu.Unlock()
	if err := r.TryRunCleanupAsync(); !errors.Is(err, ErrStorageBusy) {
		t.Errorf("got %v, want ErrStorageBusy", err)
	}
}

func TestTryRunCleanupAsync_SpawnsGoroutine(t *testing.T) {
	db := openTestStorageDB(t)
	r := &Recorder{db: db}
	if err := r.TryRunCleanupAsync(); err != nil {
		t.Fatal(err)
	}
	// Wait until we can grab the lock again — the goroutine must release it.
	deadline := time.After(time.Second)
	for {
		if r.segmentOpMu.TryLock() {
			r.segmentOpMu.Unlock()
			return
		}
		select {
		case <-deadline:
			t.Fatal("goroutine did not release lock")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
