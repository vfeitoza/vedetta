package storage

import (
	"database/sql"
	"time"
)

// Person represents a known or unknown person in the face recognition system.
type Person struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Ignore        bool      `json:"ignore"`
	Centroid      []byte    `json:"-"`
	SourceEventID string    `json:"source_event_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// Face represents a detected face with its embedding and metadata.
type Face struct {
	ID         int64     `json:"id"`
	EventID    string    `json:"event_id"`
	Camera     string    `json:"camera"`
	PersonID   *int64    `json:"person_id"`
	Embedding  []byte    `json:"-"`
	CropPath   string    `json:"crop_path,omitempty"`
	Confidence float64   `json:"confidence"`
	Similarity *float64  `json:"similarity"`
	Timestamp  time.Time `json:"timestamp"`
	CreatedAt  time.Time `json:"created_at"`
}

// SavePerson creates a new person record and returns the assigned ID.
func (d *DB) SavePerson(name string, ignore bool, centroid []byte) (int64, error) {
	return d.SavePersonWithEvent(name, ignore, centroid, "")
}

func (d *DB) SavePersonWithEvent(name string, ignore bool, centroid []byte, sourceEventID string) (int64, error) {
	result, err := d.db.Exec(
		"INSERT INTO people (name, ignore, centroid, source_event_id) VALUES (?, ?, ?, ?)",
		name, ignore, centroid, nullString(sourceEventID),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetPerson returns a person by ID, or nil if not found.
func (d *DB) GetPerson(id int64) (*Person, error) {
	row := d.db.QueryRow(
		"SELECT id, name, ignore, centroid, source_event_id, created_at FROM people WHERE id = ?", id)

	var p Person
	var name, sourceEventID sql.NullString
	var centroid []byte
	err := row.Scan(&p.ID, &name, &p.Ignore, &centroid, &sourceEventID, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Name = name.String
	p.Centroid = centroid
	p.SourceEventID = sourceEventID.String
	return &p, nil
}

// ListPeople returns all people ordered by name.
func (d *DB) ListPeople() ([]Person, error) {
	rows, err := d.db.Query(
		"SELECT id, name, ignore, centroid, source_event_id, created_at FROM people ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var people []Person
	for rows.Next() {
		var p Person
		var name, sourceEventID sql.NullString
		var centroid []byte
		if err := rows.Scan(&p.ID, &name, &p.Ignore, &centroid, &sourceEventID, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Name = name.String
		p.Centroid = centroid
		p.SourceEventID = sourceEventID.String
		people = append(people, p)
	}
	return people, rows.Err()
}

// UpdatePersonCentroid updates the centroid embedding for a person.
func (d *DB) UpdatePersonCentroid(id int64, centroid []byte) error {
	_, err := d.db.Exec("UPDATE people SET centroid = ? WHERE id = ?", centroid, id)
	return err
}

// UpdatePersonName updates the name for a person.
func (d *DB) UpdatePersonName(id int64, name string) error {
	_, err := d.db.Exec("UPDATE people SET name = ? WHERE id = ?", name, id)
	return err
}

// SaveFace inserts a face record and returns the assigned ID.
func (d *DB) SaveFace(face Face) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO faces (event_id, camera, person_id, embedding, crop_path, confidence, similarity, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullString(face.EventID), face.Camera, face.PersonID,
		face.Embedding, nullString(face.CropPath),
		face.Confidence, face.Similarity, utc(face.Timestamp),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ListFacesByPerson returns all faces assigned to a given person, ordered by timestamp descending.
func (d *DB) ListFacesByPerson(personID int64, limit int) ([]Face, error) {
	query := `
		SELECT id, event_id, camera, person_id, embedding, crop_path, confidence, similarity, timestamp, created_at
		FROM faces WHERE person_id = ?
		ORDER BY timestamp DESC`
	args := []any{personID}

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanFaces(rows)
}

// ListUnmatchedFaces returns faces without a person assignment.
func (d *DB) ListUnmatchedFaces(limit int) ([]Face, error) {
	query := `
		SELECT id, event_id, camera, person_id, embedding, crop_path, confidence, similarity, timestamp, created_at
		FROM faces WHERE person_id IS NULL
		ORDER BY timestamp DESC`
	args := []any{}

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanFaces(rows)
}

// UpdateFacePerson assigns a face to a person with the given similarity score.
func (d *DB) UpdateFacePerson(faceID, personID int64, similarity float64) error {
	_, err := d.db.Exec(
		"UPDATE faces SET person_id = ?, similarity = ? WHERE id = ?",
		personID, similarity, faceID,
	)
	return err
}

// DeletePerson removes a person record by ID.
func (d *DB) DeletePerson(id int64) error {
	_, err := d.db.Exec("DELETE FROM people WHERE id = ?", id)
	return err
}

// SetPersonIgnore updates the ignore flag for a person.
func (d *DB) SetPersonIgnore(id int64, ignore bool) error {
	_, err := d.db.Exec("UPDATE people SET ignore = ? WHERE id = ?", ignore, id)
	return err
}

// MergePeople merges person sourceID into targetID: reassigns all faces and deletes the source person.
func (d *DB) MergePeople(targetID, sourceID int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Reassign all faces from source to target
	if _, err := tx.Exec("UPDATE faces SET person_id = ? WHERE person_id = ?", targetID, sourceID); err != nil {
		return err
	}
	// Delete the source person
	if _, err := tx.Exec("DELETE FROM people WHERE id = ?", sourceID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetFaceCropPath returns the crop_path for a face by ID.
func (d *DB) DeleteFace(id int64) error {
	_, err := d.db.Exec("DELETE FROM faces WHERE id = ?", id)
	return err
}

func (d *DB) GetFaceCropPath(id int64) (string, error) {
	var path sql.NullString
	err := d.db.QueryRow("SELECT crop_path FROM faces WHERE id = ?", id).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return path.String, nil
}

// FaceEventIDs returns the distinct event IDs that already have face records.
func (d *DB) FaceEventIDs() ([]string, error) {
	rows, err := d.db.Query("SELECT DISTINCT event_id FROM faces WHERE event_id IS NOT NULL AND event_id != ''")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
