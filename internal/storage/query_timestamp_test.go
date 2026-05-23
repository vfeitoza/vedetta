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

// TestListObjectSightings_OrdersMixedFormatsAfterMigration reproduces the
// mis-ordering that occurs when a version 1 database holds object_sightings rows
// in legacy RFC3339 ("T"-separated) form alongside canonically stored rows.
// ListObjectSightings orders by s.timestamp DESC, but the "T" (0x54) sorts after
// the canonical space (0x20), so an older "T" row is returned ahead of a newer
// canonical one. The version 2 migration must canonicalize object_sightings.timestamp
// so the descending order reflects real time.
func TestListObjectSightings_OrdersMixedFormatsAfterMigration(t *testing.T) {
	raw, _ := openRaw(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// A known object for the sightings to reference.
	if _, err := raw.Exec(
		`INSERT INTO known_objects (id, name, label) VALUES (?, ?, ?)`,
		1, "car1", "car",
	); err != nil {
		t.Fatal(err)
	}
	// Pretend the DB predates version 2.
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	// Older sighting (03:00, similarity 0.1) stored in legacy "T" form; newer
	// sighting (05:00, similarity 0.9) stored canonically. Lexicographically the
	// "T" row sorts highest, so a bare DESC order returns the older row first
	// until the migration canonicalizes it. event_id is NULL to avoid the
	// events foreign key.
	if _, err := raw.Exec(
		`INSERT INTO object_sightings (event_id, camera, object_id, similarity, timestamp) VALUES (NULL, ?, ?, ?, ?)`,
		"cam1", 1, 0.1, "2024-01-02T03:00:00Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO object_sightings (event_id, camera, object_id, similarity, timestamp) VALUES (NULL, ?, ?, ?, ?)`,
		"cam1", 1, 0.9, "2024-01-02 05:00:00 +0000 UTC",
	); err != nil {
		t.Fatal(err)
	}

	if err := migrate(raw); err != nil {
		t.Fatalf("v2 migrate: %v", err)
	}

	db := &DB{db: raw}
	got, err := db.ListObjectSightings(1, 0)
	if err != nil {
		t.Fatalf("ListObjectSightings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sightings, want 2", len(got))
	}
	if got[0].Similarity != 0.9 {
		t.Errorf("DESC order returned similarity %v first, want the newer sighting (0.9); legacy row not canonicalized?", got[0].Similarity)
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
