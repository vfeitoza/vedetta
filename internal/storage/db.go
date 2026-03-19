package storage

import (
	"database/sql"
	"fmt"

	"github.com/rvben/watchpost/internal/camera"
	_ "modernc.org/sqlite"
)

// DB wraps SQLite for event storage.
type DB struct {
	db *sql.DB
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrent read/write performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			camera TEXT NOT NULL,
			label TEXT NOT NULL,
			score REAL NOT NULL,
			box_x1 INTEGER,
			box_y1 INTEGER,
			box_x2 INTEGER,
			box_y2 INTEGER,
			timestamp DATETIME NOT NULL,
			snapshot_path TEXT,
			clip_path TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_events_camera ON events(camera);
		CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_events_label ON events(label);
	`)
	return err
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) SaveEvent(event camera.Event) error {
	_, err := d.db.Exec(`
		INSERT INTO events (id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, snapshot_path, clip_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.CameraName, event.Label, event.Score,
		event.Box[0], event.Box[1], event.Box[2], event.Box[3],
		event.Timestamp, event.SnapshotPath, event.ClipPath,
	)
	return err
}

func (d *DB) UpdateEventClipPath(eventID, clipPath string) error {
	_, err := d.db.Exec("UPDATE events SET clip_path = ? WHERE id = ?", clipPath, eventID)
	return err
}

func (d *DB) UpdateEventSnapshotPath(eventID, snapshotPath string) error {
	_, err := d.db.Exec("UPDATE events SET snapshot_path = ? WHERE id = ?", snapshotPath, eventID)
	return err
}

// QueryEvents returns events matching the given filters.
func (d *DB) QueryEvents(cameraName, label string, limit int) ([]camera.Event, error) {
	query := "SELECT id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, snapshot_path, clip_path FROM events WHERE 1=1"
	args := []any{}

	if cameraName != "" {
		query += " AND camera = ?"
		args = append(args, cameraName)
	}
	if label != "" {
		query += " AND label = ?"
		args = append(args, label)
	}

	query += " ORDER BY timestamp DESC"

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []camera.Event
	for rows.Next() {
		var e camera.Event
		var snapshot, clip sql.NullString
		err := rows.Scan(&e.ID, &e.CameraName, &e.Label, &e.Score,
			&e.Box[0], &e.Box[1], &e.Box[2], &e.Box[3],
			&e.Timestamp, &snapshot, &clip,
		)
		if err != nil {
			return nil, err
		}
		e.SnapshotPath = snapshot.String
		e.ClipPath = clip.String
		events = append(events, e)
	}

	return events, rows.Err()
}
