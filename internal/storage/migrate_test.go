package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// openRaw opens a raw *sql.DB on a temp file without running migrate, so tests
// can construct legacy schemas and drive migrate directly.
func openRaw(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raw.db")
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

func mustUserVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return v
}

// TestMigrate_FreshDBStampsCurrentVersion verifies a brand-new database is
// stamped with the current schema version after migration.
func TestMigrate_FreshDBStampsCurrentVersion(t *testing.T) {
	db, _ := openRaw(t)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if got := mustUserVersion(t, db); got != currentSchemaVersion {
		t.Errorf("user_version = %d, want %d", got, currentSchemaVersion)
	}
}

// TestMigrate_Idempotent verifies running migrate repeatedly on the same DB is
// a no-op that neither errors nor changes the version.
func TestMigrate_Idempotent(t *testing.T) {
	db, _ := openRaw(t)

	for i := 0; i < 3; i++ {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate pass %d: %v", i, err)
		}
	}
	if got := mustUserVersion(t, db); got != currentSchemaVersion {
		t.Errorf("user_version = %d, want %d", got, currentSchemaVersion)
	}
}

// TestMigrate_UpgradesLegacySchema simulates a database created before column
// versioning: a minimal events table missing every later-added column. After
// migrate the columns must exist, the version must be stamped, and writes must
// succeed.
func TestMigrate_UpgradesLegacySchema(t *testing.T) {
	db, _ := openRaw(t)

	// Pre-versioning events table: only the original columns.
	if _, err := db.Exec(`
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			camera TEXT NOT NULL,
			label TEXT NOT NULL,
			score REAL NOT NULL,
			box_x1 INTEGER, box_y1 INTEGER, box_x2 INTEGER, box_y2 INTEGER,
			timestamp DATETIME NOT NULL,
			snapshot_path TEXT,
			clip_path TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, col := range []string{"end_time", "zone_name", "snapshot_available", "clip_available", "object_name", "sub_label"} {
		exists, err := columnExists(db, "events", col)
		if err != nil {
			t.Fatalf("columnExists(%s): %v", col, err)
		}
		if !exists {
			t.Errorf("expected legacy upgrade to add events.%s", col)
		}
	}

	if got := mustUserVersion(t, db); got != currentSchemaVersion {
		t.Errorf("user_version = %d, want %d", got, currentSchemaVersion)
	}

	// A normal write must work against the upgraded schema.
	wrapped := &DB{db: db}
	if err := wrapped.SaveEvent(camera.Event{
		ID:         "e1",
		CameraName: "cam1",
		Label:      "person",
		Score:      0.9,
		Box:        [4]int{1, 2, 3, 4},
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("SaveEvent after upgrade: %v", err)
	}
}

// TestMigrate_ConcurrentLegacyUpgrade reproduces two processes migrating the
// same pre-versioned database at once. Both can read user_version=0 and observe
// a legacy column as missing before either ALTER commits; the loser must
// tolerate the resulting duplicate-column error rather than fail startup.
func TestMigrate_ConcurrentLegacyUpgrade(t *testing.T) {
	seed, path := openRaw(t)
	// Legacy events table missing every later-added column, so all 13
	// backfill ALTERs contend.
	if _, err := seed.Exec(`
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			camera TEXT NOT NULL,
			label TEXT NOT NULL,
			score REAL NOT NULL,
			box_x1 INTEGER, box_y1 INTEGER, box_x2 INTEGER, box_y2 INTEGER,
			timestamp DATETIME NOT NULL,
			snapshot_path TEXT,
			clip_path TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatal(err)
	}

	// Each worker opens its own *sql.DB (its own connection pool) on the same
	// file, mirroring separate processes: WAL lets them all read table_info
	// concurrently and observe the same missing columns before any ALTER wins.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"

	const workers = 8
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			conn, err := sql.Open("sqlite", dsn)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = conn.Close() }()
			<-start // align goroutines to maximize the check-then-alter race
			errs <- migrate(conn)
		}()
	}
	close(start)
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent migrate failed: %v", err)
		}
	}

	if got := mustUserVersion(t, seed); got != currentSchemaVersion {
		t.Errorf("user_version = %d, want %d", got, currentSchemaVersion)
	}
}

// TestColumnExists verifies detection of present and absent columns.
func TestColumnExists(t *testing.T) {
	db, _ := openRaw(t)
	if _, err := db.Exec(`CREATE TABLE t (a INTEGER, b TEXT)`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		col  string
		want bool
	}{
		{"a", true},
		{"b", true},
		{"c", false},
	} {
		got, err := columnExists(db, "t", tc.col)
		if err != nil {
			t.Fatalf("columnExists(%q): %v", tc.col, err)
		}
		if got != tc.want {
			t.Errorf("columnExists(%q) = %v, want %v", tc.col, got, tc.want)
		}
	}
}

// TestEnsureColumn_SurfacesRealErrors verifies the helper no longer swallows
// genuine failures: adding a column to a non-existent table must error rather
// than be silently ignored. Adding an already-present column is a clean no-op.
func TestEnsureColumn_SurfacesRealErrors(t *testing.T) {
	db, _ := openRaw(t)
	if _, err := db.Exec(`CREATE TABLE t (a INTEGER)`); err != nil {
		t.Fatal(err)
	}

	// Already-present column: no-op, no error.
	if err := ensureColumn(db, "t", "a", "ALTER TABLE t ADD COLUMN a INTEGER"); err != nil {
		t.Errorf("ensureColumn on existing column should be a no-op, got %v", err)
	}

	// Missing column on an existing table: applied cleanly.
	if err := ensureColumn(db, "t", "b", "ALTER TABLE t ADD COLUMN b TEXT"); err != nil {
		t.Errorf("ensureColumn adding a new column: %v", err)
	}
	exists, err := columnExists(db, "t", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected column b to be added")
	}

	// Non-existent table: must surface the error, not swallow it.
	if err := ensureColumn(db, "missing_table", "x", "ALTER TABLE missing_table ADD COLUMN x TEXT"); err == nil {
		t.Error("ensureColumn against a non-existent table must return an error")
	}
}
