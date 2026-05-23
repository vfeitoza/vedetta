package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// ListZones returns all zones for a camera.
func (d *DB) ListZones(cameraName string) ([]camera.Zone, error) {
	rows, err := d.db.Query(`
		SELECT id, camera, name, points, x1, y1, x2, y2, labels, track_presence, face_recognition, enabled
		FROM zones WHERE camera = ? ORDER BY name`, cameraName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanZones(rows)
}

// GetZone returns a single zone by camera and name, or nil if not found.
func (d *DB) GetZone(cameraName, name string) (*camera.Zone, error) {
	row := d.db.QueryRow(`
		SELECT id, camera, name, points, x1, y1, x2, y2, labels, track_presence, face_recognition, enabled
		FROM zones WHERE camera = ? AND name = ?`, cameraName, name)

	z, err := scanZone(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return z, nil
}

// SaveZone upserts a zone by camera+name.
func (d *DB) SaveZone(z camera.Zone) error {
	if len(z.Points) == 0 {
		z.Points = [][]float64{
			{z.X1, z.Y1},
			{z.X2, z.Y1},
			{z.X2, z.Y2},
			{z.X1, z.Y2},
		}
	}
	labelsJSON, err := json.Marshal(z.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	pointsJSON, err := json.Marshal(z.Points)
	if err != nil {
		return fmt.Errorf("marshal points: %w", err)
	}
	x1, y1, x2, y2 := zoneBounds(z.Points)

	_, err = d.db.Exec(`
		INSERT INTO zones (camera, name, points, x1, y1, x2, y2, labels, track_presence, face_recognition, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(camera, name) DO UPDATE SET
			points = excluded.points,
			x1 = excluded.x1,
			y1 = excluded.y1,
			x2 = excluded.x2,
			y2 = excluded.y2,
			labels = excluded.labels,
			track_presence = excluded.track_presence,
			face_recognition = excluded.face_recognition,
			enabled = excluded.enabled`,
		z.Camera, z.Name, string(pointsJSON), x1, y1, x2, y2,
		string(labelsJSON), z.TrackPresence, z.FaceRecognition, z.Enabled,
	)
	return err
}

// DeleteZone removes a zone by camera and name.
func (d *DB) DeleteZone(cameraName, name string) error {
	_, err := d.db.Exec("DELETE FROM zones WHERE camera = ? AND name = ?", cameraName, name)
	return err
}

// GetZonePresence returns the presence state for all labels in a zone.
func (d *DB) GetZonePresence(zoneID int) ([]camera.ZonePresence, error) {
	rows, err := d.db.Query(`
		SELECT zone_id, label, present, last_seen, last_changed
		FROM zone_presence WHERE zone_id = ?`, zoneID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []camera.ZonePresence
	for rows.Next() {
		var zp camera.ZonePresence
		var lastSeen, lastChanged sql.NullTime
		if err := rows.Scan(&zp.ZoneID, &zp.Label, &zp.Present, &lastSeen, &lastChanged); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			zp.LastSeen = lastSeen.Time
		}
		if lastChanged.Valid {
			zp.LastChanged = lastChanged.Time
		}
		result = append(result, zp)
	}
	return result, rows.Err()
}

// UpdateZonePresence upserts a zone presence record.
func (d *DB) UpdateZonePresence(zoneID int, label string, present bool) error {
	now := utc(time.Now())
	_, err := d.db.Exec(`
		INSERT INTO zone_presence (zone_id, label, present, last_seen, last_changed)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(zone_id, label) DO UPDATE SET
			present = excluded.present,
			last_seen = excluded.last_seen,
			last_changed = CASE WHEN zone_presence.present != excluded.present THEN excluded.last_changed ELSE zone_presence.last_changed END`,
		zoneID, label, present, now, now,
	)
	return err
}

// LatestObjectNameForZone returns the object_name from the most recent event
// matching the given zone and label. A clean absence of matching events yields
// ("", nil); a genuine query failure is returned so callers do not mistake an
// error for "no object".
func (d *DB) LatestObjectNameForZone(zoneName, label string) (string, error) {
	var name sql.NullString
	err := d.db.QueryRow(`SELECT object_name FROM events WHERE zone_name = ? AND label = ? AND object_name IS NOT NULL AND object_name != '' ORDER BY timestamp DESC LIMIT 1`, zoneName, label).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return name.String, nil
}

// UpdateEventZone sets the zone_name on an event.
func (d *DB) UpdateEventZone(eventID, zoneName string) error {
	_, err := d.db.Exec("UPDATE events SET zone_name = ? WHERE id = ?", zoneName, eventID)
	return err
}

func scanZones(rows *sql.Rows) ([]camera.Zone, error) {
	var zones []camera.Zone
	for rows.Next() {
		var z camera.Zone
		var pointsJSON, labelsJSON string
		err := rows.Scan(&z.ID, &z.Camera, &z.Name, &pointsJSON, &z.X1, &z.Y1, &z.X2, &z.Y2,
			&labelsJSON, &z.TrackPresence, &z.FaceRecognition, &z.Enabled)
		if err != nil {
			return nil, err
		}
		if err := decodeZonePoints(&z, pointsJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(labelsJSON), &z.Labels); err != nil {
			slog.Warn("corrupted zone labels JSON", "zone", z.Name, "camera", z.Camera, "error", err)
			z.Labels = nil
		}
		zones = append(zones, z)
	}
	return zones, rows.Err()
}

func scanZone(row *sql.Row) (*camera.Zone, error) {
	var z camera.Zone
	var pointsJSON, labelsJSON string
	err := row.Scan(&z.ID, &z.Camera, &z.Name, &pointsJSON, &z.X1, &z.Y1, &z.X2, &z.Y2,
		&labelsJSON, &z.TrackPresence, &z.FaceRecognition, &z.Enabled)
	if err != nil {
		return nil, err
	}
	if err := decodeZonePoints(&z, pointsJSON); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(labelsJSON), &z.Labels); err != nil {
		slog.Warn("corrupted zone labels JSON", "zone", z.Name, "camera", z.Camera, "error", err)
		z.Labels = nil
	}
	return &z, nil
}
