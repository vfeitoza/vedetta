package storage

import (
	"testing"
	"time"
)

// TestQueries_LegacyRFC3339RowAfterMigration verifies the end-to-end contract
// of F8: a row stored in legacy RFC3339 ("T"-separated) form is returned by the
// index-using range queries once the version 2 migration has canonicalized it.
// Without the migration the bare "start_time < ?" comparison would mis-order
// the "T" (0x54) against the canonical space (0x20) and silently drop the row.
func TestQueries_LegacyRFC3339RowAfterMigration(t *testing.T) {
	raw, _ := openRaw(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Pretend the DB predates version 2 and holds a raw RFC3339 segment.
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO segments (camera, path, start_time, end_time, size_bytes) VALUES (?, ?, ?, ?, ?)`,
		"cam1", "cam1/seg.mp4", "2024-01-02T03:00:00Z", "2024-01-02T03:10:00Z", 100,
	); err != nil {
		t.Fatal(err)
	}
	// Run migration again: version 2 canonicalizes the legacy row.
	if err := migrate(raw); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}

	db := &DB{db: raw}

	from := time.Date(2024, 1, 2, 3, 4, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 3, 6, 0, 0, time.UTC)
	segs, err := db.QuerySegments("cam1", from, to)
	if err != nil {
		t.Fatalf("QuerySegments: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("QuerySegments returned %d segments, want 1 (legacy row not canonicalized?)", len(segs))
	}
}

// TestGetSegmentsEndingBefore_ExcludesExactCutoff verifies the de-replace()d
// retention query uses strictly-before semantics now that the parameter is
// bound in the same canonical form as the stored value. A segment ending
// exactly at the cutoff must not be selected for deletion.
func TestGetSegmentsEndingBefore_ExcludesExactCutoff(t *testing.T) {
	raw, _ := openRaw(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db := &DB{db: raw}

	end := time.Date(2024, 1, 2, 3, 0, 0, 0, time.UTC)
	if err := db.SaveSegment(SegmentRecord{
		Camera:    "cam1",
		Path:      "cam1/exact.mp4",
		StartTime: end.Add(-time.Minute),
		EndTime:   end,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSegment(SegmentRecord{
		Camera:    "cam1",
		Path:      "cam1/older.mp4",
		StartTime: end.Add(-2 * time.Minute),
		EndTime:   end.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// Cutoff == the first segment's end_time: only the strictly-earlier segment
	// qualifies.
	got, err := db.GetSegmentsEndingBefore(end)
	if err != nil {
		t.Fatalf("GetSegmentsEndingBefore: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d segments, want 1 (exact-cutoff segment must be excluded)", len(got))
	}
	if got[0].Path != "cam1/older.mp4" {
		t.Errorf("returned %q, want the strictly-earlier segment cam1/older.mp4", got[0].Path)
	}
}
