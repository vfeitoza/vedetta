package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/rvben/watchpost/internal/camera"
	_ "modernc.org/sqlite"
)

// SegmentRecord represents a recorded video segment stored in the database.
type SegmentRecord struct {
	ID        int64
	Camera    string
	Path      string
	StartTime time.Time
	EndTime   time.Time
	SizeBytes int64
}

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

		CREATE TABLE IF NOT EXISTS segments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			camera TEXT NOT NULL,
			path TEXT NOT NULL UNIQUE,
			start_time DATETIME NOT NULL,
			end_time DATETIME NOT NULL,
			size_bytes INTEGER DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_segments_camera_time ON segments(camera, start_time);
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

// SaveSegment inserts or replaces a segment record in the database.
func (d *DB) SaveSegment(seg SegmentRecord) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO segments (camera, path, start_time, end_time, size_bytes)
		VALUES (?, ?, ?, ?, ?)`,
		seg.Camera, seg.Path, seg.StartTime, seg.EndTime, seg.SizeBytes,
	)
	return err
}

// QuerySegments returns segments for a camera that overlap the given time range.
func (d *DB) QuerySegments(cameraName string, from, to time.Time) ([]SegmentRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, camera, path, start_time, end_time, size_bytes
		FROM segments
		WHERE camera = ? AND start_time < ? AND end_time > ?
		ORDER BY start_time`,
		cameraName, to, from,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSegments(rows)
}

// DeleteSegment removes a segment record by path.
func (d *DB) DeleteSegment(path string) error {
	_, err := d.db.Exec("DELETE FROM segments WHERE path = ?", path)
	return err
}

// GetAllSegments returns all segment records for a given camera.
func (d *DB) GetAllSegments(cameraName string) ([]SegmentRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, camera, path, start_time, end_time, size_bytes
		FROM segments
		WHERE camera = ?
		ORDER BY start_time`,
		cameraName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSegments(rows)
}

// GetSegmentByPath returns a single segment record by its file path, or nil if not found.
func (d *DB) GetSegmentByPath(path string) (*SegmentRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, camera, path, start_time, end_time, size_bytes
		FROM segments WHERE path = ?`, path)

	var seg SegmentRecord
	err := row.Scan(&seg.ID, &seg.Camera, &seg.Path, &seg.StartTime, &seg.EndTime, &seg.SizeBytes)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &seg, nil
}

// CountEventsToday returns the number of events with timestamp >= today midnight UTC.
func (d *DB) CountEventsToday() (int, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM events WHERE timestamp >= ?", today).Scan(&count)
	return count, err
}

// GetEventByID returns a single event by ID, or nil if not found.
func (d *DB) GetEventByID(id string) (*camera.Event, error) {
	row := d.db.QueryRow(`
		SELECT id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, snapshot_path, clip_path
		FROM events WHERE id = ?`, id)

	var e camera.Event
	var snapshot, clip sql.NullString
	err := row.Scan(&e.ID, &e.CameraName, &e.Label, &e.Score,
		&e.Box[0], &e.Box[1], &e.Box[2], &e.Box[3],
		&e.Timestamp, &snapshot, &clip,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.SnapshotPath = snapshot.String
	e.ClipPath = clip.String
	return &e, nil
}

// TotalStorageBytes returns the sum of size_bytes across all segments.
func (d *DB) TotalStorageBytes() (int64, error) {
	var total sql.NullInt64
	err := d.db.QueryRow("SELECT SUM(size_bytes) FROM segments").Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// GetSegmentsForDate returns segments for a camera where start_time falls on the given date.
func (d *DB) GetSegmentsForDate(cameraName string, date time.Time) ([]SegmentRecord, error) {
	dayStart := date.UTC().Truncate(24 * time.Hour)
	dayEnd := dayStart.Add(24 * time.Hour)

	rows, err := d.db.Query(`
		SELECT id, camera, path, start_time, end_time, size_bytes
		FROM segments
		WHERE camera = ? AND start_time >= ? AND start_time < ?
		ORDER BY start_time`,
		cameraName, dayStart, dayEnd,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSegments(rows)
}

// CountSegments returns the total number of segments.
func (d *DB) CountSegments() (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM segments").Scan(&count)
	return count, err
}

// TotalSegmentBytes returns the total bytes across all segments.
func (d *DB) TotalSegmentBytes() (int64, error) {
	return d.TotalStorageBytes()
}

// SegmentBytesByCamera returns total bytes grouped by camera name.
func (d *DB) SegmentBytesByCamera() (map[string]int64, error) {
	rows, err := d.db.Query("SELECT camera, SUM(size_bytes) FROM segments GROUP BY camera")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var cam string
		var total sql.NullInt64
		if err := rows.Scan(&cam, &total); err != nil {
			return nil, err
		}
		result[cam] = total.Int64
	}
	return result, rows.Err()
}

// GetOldestSegments returns the N oldest segments across all cameras, ordered by start_time.
func (d *DB) GetOldestSegments(limit int) ([]SegmentRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, camera, path, start_time, end_time, size_bytes
		FROM segments
		ORDER BY start_time ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSegments(rows)
}

func scanSegments(rows *sql.Rows) ([]SegmentRecord, error) {
	var segments []SegmentRecord
	for rows.Next() {
		var seg SegmentRecord
		if err := rows.Scan(&seg.ID, &seg.Camera, &seg.Path, &seg.StartTime, &seg.EndTime, &seg.SizeBytes); err != nil {
			return nil, err
		}
		segments = append(segments, seg)
	}
	return segments, rows.Err()
}
