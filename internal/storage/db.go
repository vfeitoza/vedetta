package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	_ "modernc.org/sqlite"
)

// needsNormalization returns true if a stored timestamp string contains
// non-UTC timezone info or monotonic clock readings.
func needsNormalization(s string) bool {
	return strings.Contains(s, "m=+") || strings.Contains(s, "m=-") ||
		(strings.Contains(s, "+") && !strings.HasSuffix(strings.TrimSpace(s), "+0000 UTC"))
}

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
	// PRAGMAs in the DSN are applied to every new connection in the pool.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// WAL mode permits one writer + multiple readers simultaneously.
	// busy_timeout (5s in DSN) retries when the write lock is held.
	// Default pool size is unlimited; the Go sql.DB pool handles reuse.
	// We only set idle connection limits to avoid resource waste.
	db.SetMaxIdleConns(4)

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
			end_time DATETIME,
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
	if err != nil {
		return err
	}

	// Add end_time column to existing databases
	_, _ = db.Exec("ALTER TABLE events ADD COLUMN end_time DATETIME")

	// Normalize timestamps to UTC RFC3339 format for consistent SQLite comparisons.
	// The modernc.org/sqlite driver stores time.Time using Go's String() which includes
	// timezone and monotonic clock, breaking text-based comparisons across timezones.
	normalizeTimestamps(db)

	return nil
}

func normalizeTimestamps(db *sql.DB) {
	// Check if normalization is needed by looking at a sample segment
	var sample string
	err := db.QueryRow("SELECT start_time FROM segments LIMIT 1").Scan(&sample)
	if err != nil || !needsNormalization(sample) {
		return
	}

	// Normalize segments
	rows, err := db.Query("SELECT id, start_time, end_time FROM segments")
	if err != nil {
		return
	}
	type segTime struct {
		id    int64
		start time.Time
		end   time.Time
	}
	var segs []segTime
	for rows.Next() {
		var s segTime
		if err := rows.Scan(&s.id, &s.start, &s.end); err == nil {
			segs = append(segs, s)
		}
	}
	rows.Close()

	for _, s := range segs {
		db.Exec("UPDATE segments SET start_time = ?, end_time = ? WHERE id = ?",
			s.start.UTC().Round(0), s.end.UTC().Round(0), s.id)
	}

	// Normalize events
	erows, err := db.Query("SELECT id, timestamp, end_time FROM events")
	if err != nil {
		return
	}
	type evtTime struct {
		id      string
		ts      time.Time
		endTime sql.NullTime
	}
	var evts []evtTime
	for erows.Next() {
		var e evtTime
		if err := erows.Scan(&e.id, &e.ts, &e.endTime); err == nil {
			evts = append(evts, e)
		}
	}
	erows.Close()

	for _, e := range evts {
		if e.endTime.Valid {
			db.Exec("UPDATE events SET timestamp = ?, end_time = ? WHERE id = ?",
				e.ts.UTC().Round(0), e.endTime.Time.UTC().Round(0), e.id)
		} else {
			db.Exec("UPDATE events SET timestamp = ? WHERE id = ?",
				e.ts.UTC().Round(0), e.id)
		}
	}
}

func (d *DB) Close() error {
	return d.db.Close()
}

// Ping checks database connectivity by executing a simple query.
func (d *DB) Ping() error {
	var n int
	return d.db.QueryRow("SELECT 1").Scan(&n)
}

func (d *DB) SaveEvent(event camera.Event) error {
	var endTime *time.Time
	if !event.EndTime.IsZero() {
		t := utc(event.EndTime)
		endTime = &t
	}
	_, err := d.db.Exec(`
		INSERT INTO events (id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, end_time, snapshot_path, clip_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.CameraName, event.Label, event.Score,
		event.Box[0], event.Box[1], event.Box[2], event.Box[3],
		utc(event.Timestamp), endTime, event.SnapshotPath, event.ClipPath,
	)
	return err
}

func (d *DB) UpdateEventEndTime(eventID string, endTime time.Time) error {
	_, err := d.db.Exec("UPDATE events SET end_time = ? WHERE id = ?", utc(endTime), eventID)
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
func (d *DB) QueryEvents(cameraName, label string, limit, offset int) ([]camera.Event, error) {
	query := "SELECT id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, end_time, snapshot_path, clip_path FROM events WHERE 1=1"
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

	if offset > 0 {
		query += " OFFSET ?"
		args = append(args, offset)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanEvents(rows)
}

// CountEventsByLabel returns the count of events grouped by label.
func (d *DB) CountEventsByLabel() (map[string]int, error) {
	rows, err := d.db.Query("SELECT label, COUNT(*) FROM events GROUP BY label")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			return nil, err
		}
		result[label] = count
	}
	return result, rows.Err()
}

// CountEventsByCamera returns the count of events grouped by camera name.
func (d *DB) CountEventsByCamera() (map[string]int, error) {
	rows, err := d.db.Query("SELECT camera, COUNT(*) FROM events GROUP BY camera")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var cam string
		var count int
		if err := rows.Scan(&cam, &count); err != nil {
			return nil, err
		}
		result[cam] = count
	}
	return result, rows.Err()
}

// CountEvents returns the total number of events.
func (d *DB) CountEvents() (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	return count, err
}

// utc normalizes a time.Time to UTC and strips the monotonic clock reading.
// This ensures consistent text representation in SQLite for correct comparisons.
func utc(t time.Time) time.Time {
	return t.UTC().Round(0)
}

// SaveSegment inserts or replaces a segment record in the database.
func (d *DB) SaveSegment(seg SegmentRecord) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO segments (camera, path, start_time, end_time, size_bytes)
		VALUES (?, ?, ?, ?, ?)`,
		seg.Camera, seg.Path, utc(seg.StartTime), utc(seg.EndTime), seg.SizeBytes,
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
		cameraName, utc(to), utc(from),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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
		SELECT id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, end_time, snapshot_path, clip_path
		FROM events WHERE id = ?`, id)

	var e camera.Event
	var endTime sql.NullTime
	var snapshot, clip sql.NullString
	err := row.Scan(&e.ID, &e.CameraName, &e.Label, &e.Score,
		&e.Box[0], &e.Box[1], &e.Box[2], &e.Box[3],
		&e.Timestamp, &endTime, &snapshot, &clip,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if endTime.Valid {
		e.EndTime = endTime.Time
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
// If cameraName is empty, returns segments for all cameras.
func (d *DB) GetSegmentsForDate(cameraName string, date time.Time) ([]SegmentRecord, error) {
	dayStart := date.UTC().Truncate(24 * time.Hour)
	dayEnd := dayStart.Add(24 * time.Hour)

	var rows *sql.Rows
	var err error
	if cameraName != "" {
		rows, err = d.db.Query(`
			SELECT id, camera, path, start_time, end_time, size_bytes
			FROM segments
			WHERE camera = ? AND start_time >= ? AND start_time < ?
			ORDER BY start_time`,
			cameraName, dayStart, dayEnd,
		)
	} else {
		rows, err = d.db.Query(`
			SELECT id, camera, path, start_time, end_time, size_bytes
			FROM segments
			WHERE start_time >= ? AND start_time < ?
			ORDER BY start_time`,
			dayStart, dayEnd,
		)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSegments(rows)
}

// QueryEventsForDate returns events for a camera on a given date (UTC day).
func (d *DB) QueryEventsForDate(cameraName string, date time.Time) ([]camera.Event, error) {
	dayStart := date.UTC().Truncate(24 * time.Hour)
	dayEnd := dayStart.Add(24 * time.Hour)

	rows, err := d.db.Query(`
		SELECT id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, end_time, snapshot_path, clip_path
		FROM events
		WHERE camera = ? AND timestamp >= ? AND timestamp < ?
		ORDER BY timestamp`,
		cameraName, dayStart, dayEnd,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanEvents(rows)
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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

	return scanSegments(rows)
}

// GetRecordingDays returns sorted day numbers that have segments for the given camera and month.
// If camera is empty, returns days across all cameras.
func (d *DB) GetRecordingDays(camera string, year int, month int) ([]int, error) {
	monthStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	var rows *sql.Rows
	var err error
	if camera != "" {
		rows, err = d.db.Query(`
			SELECT DISTINCT CAST(substr(start_time, 9, 2) AS INTEGER) AS day
			FROM segments
			WHERE camera = ? AND start_time >= ? AND start_time < ?
			ORDER BY day`, camera, monthStart, monthEnd)
	} else {
		rows, err = d.db.Query(`
			SELECT DISTINCT CAST(substr(start_time, 9, 2) AS INTEGER) AS day
			FROM segments
			WHERE start_time >= ? AND start_time < ?
			ORDER BY day`, monthStart, monthEnd)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var days []int
	for rows.Next() {
		var day int
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

// GetAdjacentEvents returns the previous and next event IDs relative to the given event,
// ordered by timestamp.
func (d *DB) GetAdjacentEvents(id string) (prevID, nextID string, err error) {
	err = d.db.QueryRow(`
		SELECT id FROM events
		WHERE timestamp < (SELECT timestamp FROM events WHERE id = ?)
		ORDER BY timestamp DESC LIMIT 1`, id).Scan(&prevID)
	if err == sql.ErrNoRows {
		prevID = ""
		err = nil
	}
	if err != nil {
		return "", "", err
	}

	err = d.db.QueryRow(`
		SELECT id FROM events
		WHERE timestamp > (SELECT timestamp FROM events WHERE id = ?)
		ORDER BY timestamp ASC LIMIT 1`, id).Scan(&nextID)
	if err == sql.ErrNoRows {
		nextID = ""
		err = nil
	}
	if err != nil {
		return "", "", err
	}

	return prevID, nextID, nil
}

func scanEvents(rows *sql.Rows) ([]camera.Event, error) {
	var events []camera.Event
	for rows.Next() {
		var e camera.Event
		var endTime sql.NullTime
		var snapshot, clip sql.NullString
		err := rows.Scan(&e.ID, &e.CameraName, &e.Label, &e.Score,
			&e.Box[0], &e.Box[1], &e.Box[2], &e.Box[3],
			&e.Timestamp, &endTime, &snapshot, &clip,
		)
		if err != nil {
			return nil, err
		}
		if endTime.Valid {
			e.EndTime = endTime.Time
		}
		e.SnapshotPath = snapshot.String
		e.ClipPath = clip.String
		events = append(events, e)
	}
	return events, rows.Err()
}

// DeleteEvent removes an event by ID.
func (d *DB) DeleteEvent(id string) error {
	_, err := d.db.Exec("DELETE FROM events WHERE id = ?", id)
	return err
}

// EventsWithSnapshots returns all events that have a non-empty snapshot_path.
func (d *DB) EventsWithSnapshots() ([]camera.Event, error) {
	rows, err := d.db.Query(`
		SELECT id, camera, label, score, box_x1, box_y1, box_x2, box_y2, timestamp, end_time, snapshot_path, clip_path
		FROM events WHERE snapshot_path != '' AND snapshot_path IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEvents(rows)
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
