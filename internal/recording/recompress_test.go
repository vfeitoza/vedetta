package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/storage"
)

func newTestRecompressor(t *testing.T) (*Recompressor, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.TieredStorageConfig{
		Enabled:      true,
		AfterDays:    1,
		TargetWidth:  1280,
		TargetHeight: 720,
		Schedule:     "02:00-05:00",
	}
	cameras := []config.CameraConfig{
		{Name: "cam1"},
	}
	r := NewRecompressor(cfg, cameras, db)
	return r, db
}

func TestRecompressionJob_SkipsAlreadyRecompressed(t *testing.T) {
	_, db := newTestRecompressor(t)
	dir := t.TempDir()

	seg := storage.SegmentRecord{
		Camera:    "cam1",
		Path:      filepath.Join(dir, "seg.mp4"),
		StartTime: time.Now().Add(-48 * time.Hour),
		EndTime:   time.Now().Add(-47 * time.Hour),
		SizeBytes: 1000,
	}
	if err := db.SaveSegment(seg); err != nil {
		t.Fatalf("SaveSegment: %v", err)
	}

	// Get the segment ID and mark it as recompressed
	all, err := db.GetAllSegments("cam1")
	if err != nil || len(all) == 0 {
		t.Fatal("expected to find saved segment")
	}
	if err := db.MarkSegmentRecompressed(all[0].ID, 500); err != nil {
		t.Fatalf("MarkSegmentRecompressed: %v", err)
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	segs, err := db.GetSegmentsForRecompression("cam1", cutoff)
	if err != nil {
		t.Fatalf("GetSegmentsForRecompression: %v", err)
	}
	if len(segs) != 0 {
		t.Errorf("expected 0 eligible segments, got %d", len(segs))
	}
}

func TestRecompressionJob_RespectsScheduleWindow(t *testing.T) {
	inside := time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local)
	outside := time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)

	ok, err := config.InScheduleWindow("02:00-05:00", inside)
	if err != nil || !ok {
		t.Errorf("expected inside=true, got %v err=%v", ok, err)
	}
	ok, err = config.InScheduleWindow("02:00-05:00", outside)
	if err != nil || ok {
		t.Errorf("expected outside=false, got %v err=%v", ok, err)
	}
}

func TestRecompressionJob_SkipsDisabledCamera(t *testing.T) {
	_, db := newTestRecompressor(t)

	// Global tiered storage is enabled, but cam_disabled has enabled: false override.
	cfg := config.TieredStorageConfig{Enabled: true, AfterDays: 1, Schedule: "02:00-05:00"}
	disabled := false
	cameras := []config.CameraConfig{
		{Name: "cam_enabled"},
		{Name: "cam_disabled", TieredStorage: config.CameraTieredStorageConfig{Enabled: &disabled}},
	}
	r := NewRecompressor(cfg, cameras, db)
	eligible := r.eligibleCameras()

	if _, ok := eligible["cam_disabled"]; ok {
		t.Error("cam_disabled should not be eligible when enabled=false")
	}
	if _, ok := eligible["cam_enabled"]; !ok {
		t.Error("cam_enabled should be eligible")
	}
}

func TestRecompressionJob_PerCameraAfterDaysOverride(t *testing.T) {
	_, db := newTestRecompressor(t)
	dir := t.TempDir()
	now := time.Now()

	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_short", Path: filepath.Join(dir, "a.mp4"),
		StartTime: now.Add(-48 * time.Hour), EndTime: now.Add(-47 * time.Hour), SizeBytes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_long", Path: filepath.Join(dir, "b.mp4"),
		StartTime: now.Add(-48 * time.Hour), EndTime: now.Add(-47 * time.Hour), SizeBytes: 1,
	}); err != nil {
		t.Fatal(err)
	}

	global := config.TieredStorageConfig{Enabled: true, AfterDays: 1, Schedule: "02:00-05:00"}
	afterLong := 5
	cameras := []config.CameraConfig{
		{Name: "cam_short"},
		{Name: "cam_long", TieredStorage: config.CameraTieredStorageConfig{AfterDays: &afterLong}},
	}
	r := NewRecompressor(global, cameras, db)

	segsShort, _ := db.GetSegmentsForRecompression("cam_short", now.Add(-time.Duration(1)*24*time.Hour))
	if len(segsShort) == 0 {
		t.Error("cam_short should have eligible segments")
	}
	segsLong, _ := db.GetSegmentsForRecompression("cam_long", now.Add(-time.Duration(5)*24*time.Hour))
	if len(segsLong) != 0 {
		t.Error("cam_long should have no eligible segments (segment too new)")
	}
	_ = r
}

func TestRecompressionJob_RetriesAfterFailure(t *testing.T) {
	_, db := newTestRecompressor(t)
	dir := t.TempDir()

	seg := storage.SegmentRecord{
		Camera: "cam1", Path: filepath.Join(dir, "seg.mp4"),
		StartTime: time.Now().Add(-48 * time.Hour), EndTime: time.Now().Add(-47 * time.Hour),
		SizeBytes: 1,
	}
	if err := db.SaveSegment(seg); err != nil {
		t.Fatal(err)
	}

	all, _ := db.GetAllSegments("cam1")
	if len(all) == 0 {
		t.Fatal("no segments found")
	}
	id := all[0].ID

	for range 3 {
		if err := db.IncrementSegmentRecompressFailures(id); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	segs, err := db.GetSegmentsForRecompression("cam1", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Errorf("expected 0 eligible after 3 failures, got %d", len(segs))
	}
}

func TestRecompressionJob_PanicRecovery(t *testing.T) {
	r, db := newTestRecompressor(t)
	dir := t.TempDir()

	segPath := filepath.Join(dir, "panic.mp4")
	if err := os.WriteFile(segPath, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	seg := storage.SegmentRecord{
		Camera: "cam1", Path: segPath,
		StartTime: time.Now().Add(-48 * time.Hour), EndTime: time.Now().Add(-47 * time.Hour),
		SizeBytes: 100,
	}
	if err := db.SaveSegment(seg); err != nil {
		t.Fatal(err)
	}

	// Inject a panicking transcode function to simulate the OpenH264 purego crash
	r.transcodeFn = func(string, int, int) (media.TranscodeResult, error) {
		panic("simulated OpenH264 purego crash")
	}

	// processOne must not panic — it should recover and increment the failure counter
	processed := r.processOne()
	if !processed {
		t.Error("expected processOne to return true (segment was attempted)")
	}

	all, _ := db.GetAllSegments("cam1")
	if len(all) == 0 {
		t.Fatal("no segments found")
	}
	if all[0].RecompressFailures != 1 {
		t.Errorf("expected 1 failure recorded, got %d", all[0].RecompressFailures)
	}
}

func TestRecompressorPicksLargestFirst(t *testing.T) {
	_, db := newTestRecompressor(t)
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-4 * 24 * time.Hour)

	pathA := filepath.Join(dir, "a.mp4")
	pathB := filepath.Join(dir, "b.mp4")
	pathC := filepath.Join(dir, "c.mp4")
	for _, p := range []string{pathA, pathB, pathC} {
		if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// cam_a: 10MB but oldest. cam_b: 500MB (largest, newer) and 100MB (newest).
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_a", Path: pathA,
		StartTime: old, EndTime: old.Add(time.Minute), SizeBytes: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_b", Path: pathB,
		StartTime: old.Add(time.Hour), EndTime: old.Add(time.Hour).Add(time.Minute), SizeBytes: 500,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_b", Path: pathC,
		StartTime: old.Add(2 * time.Hour), EndTime: old.Add(2 * time.Hour).Add(time.Minute), SizeBytes: 100,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.TieredStorageConfig{
		Enabled: true, AfterDays: 1, Schedule: "00:00-23:59",
		Interval: time.Second, Priority: "largest",
		TargetWidth: 640, TargetHeight: 360,
	}
	cams := []config.CameraConfig{{Name: "cam_a"}, {Name: "cam_b"}}
	r := NewRecompressor(cfg, cams, db)

	var transcoded []string
	r.transcodeFn = func(path string, w, h int) (media.TranscodeResult, error) {
		transcoded = append(transcoded, path)
		return media.TranscodeResult{OriginalSize: 500, NewSize: 50}, nil
	}

	if !r.processOne() {
		t.Fatal("processOne returned false, want true")
	}
	if len(transcoded) != 1 || transcoded[0] != pathB {
		t.Fatalf("first transcode = %v, want [%s]", transcoded, pathB)
	}
}

func TestRecompressorPicksOldestWhenConfigured(t *testing.T) {
	_, db := newTestRecompressor(t)
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-4 * 24 * time.Hour)

	pathA := filepath.Join(dir, "a.mp4")
	pathB := filepath.Join(dir, "b.mp4")
	for _, p := range []string{pathA, pathB} {
		if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// cam_a: 10MB, oldest. cam_b: 500MB, newer. With Priority=oldest, /a.mp4 wins.
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_a", Path: pathA,
		StartTime: old, EndTime: old.Add(time.Minute), SizeBytes: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera: "cam_b", Path: pathB,
		StartTime: old.Add(time.Hour), EndTime: old.Add(time.Hour).Add(time.Minute), SizeBytes: 500,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.TieredStorageConfig{
		Enabled: true, AfterDays: 1, Schedule: "00:00-23:59",
		Interval: time.Second, Priority: "oldest",
		TargetWidth: 640, TargetHeight: 360,
	}
	cams := []config.CameraConfig{{Name: "cam_a"}, {Name: "cam_b"}}
	r := NewRecompressor(cfg, cams, db)

	var transcoded []string
	r.transcodeFn = func(path string, w, h int) (media.TranscodeResult, error) {
		transcoded = append(transcoded, path)
		return media.TranscodeResult{OriginalSize: 10, NewSize: 5}, nil
	}

	if !r.processOne() {
		t.Fatal("processOne returned false, want true")
	}
	if len(transcoded) != 1 || transcoded[0] != pathA {
		t.Fatalf("first transcode = %v, want [%s]", transcoded, pathA)
	}
}

func TestRecompressionJob_UpdatesDBOnSuccess(t *testing.T) {
	_, db := newTestRecompressor(t)
	dir := t.TempDir()

	f, _ := os.Create(filepath.Join(dir, "seg.mp4"))
	_, _ = f.Write(make([]byte, 1000))
	f.Close()

	seg := storage.SegmentRecord{
		Camera: "cam1", Path: filepath.Join(dir, "seg.mp4"),
		StartTime: time.Now().Add(-48 * time.Hour), EndTime: time.Now().Add(-47 * time.Hour),
		SizeBytes: 1000,
	}
	if err := db.SaveSegment(seg); err != nil {
		t.Fatal(err)
	}
	all, _ := db.GetAllSegments("cam1")
	id := all[0].ID

	if err := db.MarkSegmentRecompressed(id, 500); err != nil {
		t.Fatalf("MarkSegmentRecompressed: %v", err)
	}

	updated, _ := db.GetSegmentByID(id)
	if !updated.Recompressed {
		t.Error("expected recompressed=true")
	}
	if updated.SizeBytes != 500 {
		t.Errorf("size_bytes = %d, want 500", updated.SizeBytes)
	}
	if updated.RecompressedAt.IsZero() {
		t.Error("expected recompressed_at to be set")
	}
}
