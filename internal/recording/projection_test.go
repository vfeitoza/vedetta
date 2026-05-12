package recording

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// newProjectionRecorder creates a Recorder configured with the given retain_days.
func newProjectionRecorder(t *testing.T, retainDays int) (*Recorder, *storage.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.RecordingConfig{
		Path:          filepath.Join(tmpDir, "recordings"),
		SegmentLength: 10 * time.Minute,
		RetainDays:    retainDays,
	}
	rec := New(cfg, config.EventConfig{RetainDays: 90}, nil, db, nil, "", "", nil)
	return rec, db
}

// TestProjection_EmptyDB verifies that an empty database returns a safe default
// projection with status "ok".
func TestProjection_EmptyDB(t *testing.T) {
	rec, _ := newProjectionRecorder(t, 7)

	proj := rec.computeProjection(&StorageStats{
		TotalBytes:    0,
		DiskAvailable: 1000 * 1024 * 1024 * 1024, // 1TB
	})

	if proj.Status != "ok" {
		t.Errorf("Status = %q, want %q", proj.Status, "ok")
	}
	if proj.RetainDays != 7 {
		t.Errorf("RetainDays = %d, want 7", proj.RetainDays)
	}
	if proj.DailyIngestBytes != 0 {
		t.Errorf("DailyIngestBytes = %d, want 0", proj.DailyIngestBytes)
	}
	if proj.DaysUntilFull != nil {
		t.Errorf("DaysUntilFull = %v, want nil", proj.DaysUntilFull)
	}
}

// TestProjection_StillFilling_FitsComfortably verifies that a system still
// filling with a comfortable fit reports ok status and provides days_until_full.
func TestProjection_StillFilling_FitsComfortably(t *testing.T) {
	rec, db := newProjectionRecorder(t, 7)

	// Seed segments from the last 24h: 10GB total daily ingest
	now := time.Now()
	const dailyIngest = 10 * 1024 * 1024 * 1024 // 10 GB

	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      "/tmp/p1.mp4",
		StartTime: now.Add(-12 * time.Hour),
		EndTime:   now.Add(-11 * time.Hour),
		SizeBytes: dailyIngest,
	})

	// Oldest segment is 12 hours old — still filling (< 7 days)
	stats := &StorageStats{
		TotalBytes:    dailyIngest,
		DiskAvailable: 990 * 1024 * 1024 * 1024, // ~990 GB free → ~1 TB total
	}

	proj := rec.computeProjection(stats)

	// steady state = 10GB × 7 = 70GB; disk total ~1TB — should fit easily
	if !proj.SteadyStateFits {
		t.Errorf("SteadyStateFits = false, want true")
	}
	if proj.Status != "ok" {
		t.Errorf("Status = %q, want %q", proj.Status, "ok")
	}
	if proj.DaysUntilFull == nil {
		t.Error("DaysUntilFull = nil, want a value (system still filling)")
	}
	if proj.SteadyStateBytes != int64(dailyIngest)*7 {
		t.Errorf("SteadyStateBytes = %d, want %d", proj.SteadyStateBytes, int64(dailyIngest)*7)
	}
	if proj.HeadroomBytes <= 0 {
		t.Errorf("HeadroomBytes = %d, want positive", proj.HeadroomBytes)
	}
}

// TestProjection_StillFilling_WillNotFit verifies that a system whose steady
// state exceeds disk capacity is classified as insufficient.
func TestProjection_StillFilling_WillNotFit(t *testing.T) {
	rec, db := newProjectionRecorder(t, 10)

	// 100 GB/day × 10 days = 1 TB steady state, disk is only 500 GB
	now := time.Now()
	const dailyIngest = 100 * 1024 * 1024 * 1024 // 100 GB

	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      "/tmp/p2.mp4",
		StartTime: now.Add(-12 * time.Hour),
		EndTime:   now.Add(-11 * time.Hour),
		SizeBytes: dailyIngest,
	})

	stats := &StorageStats{
		TotalBytes:    dailyIngest,
		DiskAvailable: 400 * 1024 * 1024 * 1024, // 400 GB free → ~500 GB total
	}

	proj := rec.computeProjection(stats)

	if proj.SteadyStateFits {
		t.Errorf("SteadyStateFits = true, want false")
	}
	if proj.Status != "insufficient" {
		t.Errorf("Status = %q, want %q", proj.Status, "insufficient")
	}
	if proj.HeadroomBytes >= 0 {
		t.Errorf("HeadroomBytes = %d, want negative", proj.HeadroomBytes)
	}
}

// TestProjection_SteadyStateReached verifies that days_until_full is nil when
// the oldest segment is older than retain_days (retention is fully cycling).
func TestProjection_SteadyStateReached(t *testing.T) {
	rec, db := newProjectionRecorder(t, 7)

	// Oldest segment is 10 days old — beyond retain_days=7, steady state reached
	now := time.Now()
	const dailyIngest = 5 * 1024 * 1024 * 1024 // 5 GB

	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      "/tmp/p3.mp4",
		StartTime: now.Add(-10 * 24 * time.Hour), // 10 days old
		EndTime:   now.Add(-10*24*time.Hour + time.Hour),
		SizeBytes: 100,
	})
	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      "/tmp/p3b.mp4",
		StartTime: now.Add(-12 * time.Hour),
		EndTime:   now.Add(-11 * time.Hour),
		SizeBytes: dailyIngest,
	})

	stats := &StorageStats{
		TotalBytes:    dailyIngest,
		DiskAvailable: 990 * 1024 * 1024 * 1024,
	}

	proj := rec.computeProjection(stats)

	if proj.DaysUntilFull != nil {
		t.Errorf("DaysUntilFull = %v, want nil (steady state reached)", proj.DaysUntilFull)
	}
	if proj.OldestSegmentDays < 9 {
		t.Errorf("OldestSegmentDays = %.1f, want >= 9", proj.OldestSegmentDays)
	}
}

// TestProjection_DiskLow_Critical verifies that a tripped disk_low flag
// overrides everything else and sets status to "critical".
func TestProjection_DiskLow_Critical(t *testing.T) {
	rec, db := newProjectionRecorder(t, 7)

	now := time.Now()
	db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam1",
		Path:      "/tmp/p4.mp4",
		StartTime: now.Add(-12 * time.Hour),
		EndTime:   now.Add(-11 * time.Hour),
		SizeBytes: 1024 * 1024 * 1024, // 1 GB
	})

	stats := &StorageStats{
		TotalBytes:    990 * 1024 * 1024 * 1024,
		DiskAvailable: 1 * 1024 * 1024, // 1 MB — disk very low
		DiskLow:       true,
	}

	proj := rec.computeProjection(stats)

	if proj.Status != "critical" {
		t.Errorf("Status = %q, want %q", proj.Status, "critical")
	}
}
