package recording

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// TestEmergencyDeleteHonorsMinRetention verifies that EmergencyDelete only
// removes segments older than MinRetention, leaving newer recordings intact.
func TestEmergencyDeleteHonorsMinRetention(t *testing.T) {
	rec, db := newTestRecorder(t)
	now := time.Now()
	segDir := filepath.Join(rec.config.Path, "cam", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed 5 segments: 4h, 3h, 2h, 30min, 5min old. Each 10MB.
	ages := []time.Duration{4 * time.Hour, 3 * time.Hour, 2 * time.Hour, 30 * time.Minute, 5 * time.Minute}
	for i, age := range ages {
		end := now.Add(-age)
		path := filepath.Join(segDir, fmt.Sprintf("%d.mp4", i))
		if err := os.WriteFile(path, make([]byte, 10*1024*1024), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveSegment(storage.SegmentRecord{
			Camera:    "cam",
			Path:      path,
			StartTime: end.Add(-10 * time.Minute),
			EndTime:   end,
			SizeBytes: 10 * 1024 * 1024,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	cfg := config.UrgentCleanupConfig{Enabled: true, MinRetention: time.Hour, BatchSize: 10}
	deleted, err := rec.EmergencyDelete(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EmergencyDelete: %v", err)
	}

	// Segments older than 1h are at indices 0 (4h), 1 (3h), 2 (2h) — expect 3 deleted.
	if deleted != 3 {
		t.Fatalf("deleted %d, want 3", deleted)
	}

	// Verify via DB: only segments younger than 1h should remain.
	remaining, err := db.GetAllSegments("cam")
	if err != nil {
		t.Fatalf("GetAllSegments: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining %d segments, want 2", len(remaining))
	}
	for _, seg := range remaining {
		age := now.Sub(seg.EndTime)
		if age >= time.Hour {
			t.Errorf("segment %q is %s old — should have been deleted (older than 1h floor)", seg.Path, age.Round(time.Minute))
		}
	}
}

// TestEmergencyDeleteDisabled verifies that EmergencyDelete is a no-op when
// cfg.Enabled is false, regardless of how many old segments exist.
func TestEmergencyDeleteDisabled(t *testing.T) {
	rec, db := newTestRecorder(t)
	now := time.Now()
	segDir := filepath.Join(rec.config.Path, "cam", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed one old segment that would be eligible for emergency deletion.
	path := filepath.Join(segDir, "old.mp4")
	if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam",
		Path:      path,
		StartTime: now.Add(-5 * time.Hour),
		EndTime:   now.Add(-4 * time.Hour),
		SizeBytes: 1024,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := config.UrgentCleanupConfig{Enabled: false, MinRetention: time.Hour, BatchSize: 10}
	deleted, err := rec.EmergencyDelete(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EmergencyDelete: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted %d, want 0 (Enabled=false)", deleted)
	}

	// Segment must still exist in the DB.
	seg, err := db.GetSegmentByPath(path)
	if err != nil {
		t.Fatalf("GetSegmentByPath: %v", err)
	}
	if seg == nil {
		t.Fatal("segment was removed even though Enabled=false")
	}
}

// TestEmergencyDeleteContextCanceled verifies that the loop exits early when
// the context is canceled, rather than processing the full batch.
func TestEmergencyDeleteContextCanceled(t *testing.T) {
	rec, db := newTestRecorder(t)
	now := time.Now()
	segDir := filepath.Join(rec.config.Path, "cam", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed 5 eligible segments.
	for i := range 5 {
		end := now.Add(-time.Duration(i+2) * time.Hour)
		path := filepath.Join(segDir, fmt.Sprintf("%d.mp4", i))
		if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveSegment(storage.SegmentRecord{
			Camera:    "cam",
			Path:      path,
			StartTime: end.Add(-10 * time.Minute),
			EndTime:   end,
			SizeBytes: 1024,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately before calling EmergencyDelete.

	cfg := config.UrgentCleanupConfig{Enabled: true, MinRetention: time.Hour, BatchSize: 10}
	deleted, err := rec.EmergencyDelete(ctx, cfg)
	if err != nil {
		t.Fatalf("EmergencyDelete: %v", err)
	}
	// With an already-canceled context the loop must not delete anything.
	if deleted != 0 {
		t.Fatalf("deleted %d, want 0 with pre-canceled context", deleted)
	}
}

// TestEmergencyDeleteNoCandidates verifies graceful handling when all segments
// are younger than MinRetention (nothing eligible to delete).
func TestEmergencyDeleteNoCandidates(t *testing.T) {
	rec, db := newTestRecorder(t)
	now := time.Now()
	segDir := filepath.Join(rec.config.Path, "cam", "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Only recent segments (30min old — within 1h floor).
	path := filepath.Join(segDir, "recent.mp4")
	if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(storage.SegmentRecord{
		Camera:    "cam",
		Path:      path,
		StartTime: now.Add(-40 * time.Minute),
		EndTime:   now.Add(-30 * time.Minute),
		SizeBytes: 1024,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.UrgentCleanupConfig{Enabled: true, MinRetention: time.Hour, BatchSize: 10}
	deleted, err := rec.EmergencyDelete(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EmergencyDelete: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted %d, want 0 (all segments within retention floor)", deleted)
	}
}
