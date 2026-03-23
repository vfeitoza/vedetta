package storage

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func makeEmbedding(seed float32) []byte {
	embedding := make([]float32, 512)
	for i := range embedding {
		embedding[i] = seed + float32(i)*0.001
	}
	return float32ToBytes(embedding)
}

func float32ToBytes(f []float32) []byte {
	buf := make([]byte, len(f)*4)
	for i, v := range f {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func TestSavePerson_And_GetPerson(t *testing.T) {
	db := newTestDB(t)

	centroid := makeEmbedding(1.0)
	id, err := db.SavePerson("Alice", false, centroid)
	if err != nil {
		t.Fatalf("SavePerson: %v", err)
	}
	if id <= 0 {
		t.Fatalf("SavePerson returned invalid ID: %d", id)
	}

	p, err := db.GetPerson(id)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if p == nil {
		t.Fatal("GetPerson returned nil")
	}
	if p.Name != "Alice" {
		t.Errorf("person name = %q, want Alice", p.Name)
	}
	if p.Ignore {
		t.Error("person should not be ignored")
	}
	if len(p.Centroid) != 2048 {
		t.Errorf("centroid size = %d, want 2048", len(p.Centroid))
	}
}

func TestGetPerson_NotFound(t *testing.T) {
	db := newTestDB(t)

	p, err := db.GetPerson(999)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil for non-existent person, got %+v", p)
	}
}

func TestListPeople(t *testing.T) {
	db := newTestDB(t)

	db.SavePerson("Charlie", false, nil)
	db.SavePerson("Alice", false, nil)
	db.SavePerson("Bob", true, nil)

	people, err := db.ListPeople()
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(people) != 3 {
		t.Fatalf("ListPeople returned %d, want 3", len(people))
	}

	// Should be ordered by name
	if people[0].Name != "Alice" {
		t.Errorf("first person = %q, want Alice", people[0].Name)
	}
	if people[1].Name != "Bob" {
		t.Errorf("second person = %q, want Bob", people[1].Name)
	}
	if people[2].Name != "Charlie" {
		t.Errorf("third person = %q, want Charlie", people[2].Name)
	}
}

func TestUpdatePersonCentroid(t *testing.T) {
	db := newTestDB(t)

	id, _ := db.SavePerson("Alice", false, nil)
	centroid := makeEmbedding(2.0)

	if err := db.UpdatePersonCentroid(id, centroid); err != nil {
		t.Fatalf("UpdatePersonCentroid: %v", err)
	}

	p, _ := db.GetPerson(id)
	if len(p.Centroid) != 2048 {
		t.Errorf("updated centroid size = %d, want 2048", len(p.Centroid))
	}
}

func TestUpdatePersonName(t *testing.T) {
	db := newTestDB(t)

	id, _ := db.SavePerson("Unknown", false, nil)
	if err := db.UpdatePersonName(id, "Alice"); err != nil {
		t.Fatalf("UpdatePersonName: %v", err)
	}

	p, _ := db.GetPerson(id)
	if p.Name != "Alice" {
		t.Errorf("updated name = %q, want Alice", p.Name)
	}
}

func TestSaveFace_And_ListByPerson(t *testing.T) {
	db := newTestDB(t)

	personID, _ := db.SavePerson("Alice", false, nil)

	now := time.Now().UTC()
	embedding := makeEmbedding(1.0)
	sim := 0.95

	face := Face{
		Camera:     "front_door",
		PersonID:   &personID,
		Embedding:  embedding,
		CropPath:   "/crops/face_001.jpg",
		Confidence: 0.98,
		Similarity: &sim,
		Timestamp:  now,
	}

	faceID, err := db.SaveFace(face)
	if err != nil {
		t.Fatalf("SaveFace: %v", err)
	}
	if faceID <= 0 {
		t.Fatalf("SaveFace returned invalid ID: %d", faceID)
	}

	faces, err := db.ListFacesByPerson(personID, 10)
	if err != nil {
		t.Fatalf("ListFacesByPerson: %v", err)
	}
	if len(faces) != 1 {
		t.Fatalf("ListFacesByPerson returned %d, want 1", len(faces))
	}

	f := faces[0]
	if f.Camera != "front_door" {
		t.Errorf("face camera = %q, want front_door", f.Camera)
	}
	if f.PersonID == nil || *f.PersonID != personID {
		t.Errorf("face person_id = %v, want %d", f.PersonID, personID)
	}
	if len(f.Embedding) != 2048 {
		t.Errorf("face embedding size = %d, want 2048", len(f.Embedding))
	}
	if f.Confidence != 0.98 {
		t.Errorf("face confidence = %f, want 0.98", f.Confidence)
	}
	if f.Similarity == nil || *f.Similarity != 0.95 {
		t.Errorf("face similarity = %v, want 0.95", f.Similarity)
	}
}

func TestSaveFace_WithEventID(t *testing.T) {
	db := newTestDB(t)

	// Create an event first
	now := time.Now().UTC()
	event := makeEvent("evt-001", "front_door", "person", 0.9, now)
	mustSaveEvent(t, db, event)

	face := Face{
		EventID:    "evt-001",
		Camera:     "front_door",
		Embedding:  makeEmbedding(1.0),
		Confidence: 0.85,
		Timestamp:  now,
	}

	faceID, err := db.SaveFace(face)
	if err != nil {
		t.Fatalf("SaveFace with event: %v", err)
	}
	if faceID <= 0 {
		t.Fatal("SaveFace returned invalid ID")
	}
}

func TestListUnmatchedFaces(t *testing.T) {
	db := newTestDB(t)

	personID, _ := db.SavePerson("Alice", false, nil)
	now := time.Now().UTC()

	// Matched face
	db.SaveFace(Face{
		Camera:     "cam1",
		PersonID:   &personID,
		Embedding:  makeEmbedding(1.0),
		Confidence: 0.9,
		Timestamp:  now,
	})

	// Unmatched face
	db.SaveFace(Face{
		Camera:     "cam2",
		Embedding:  makeEmbedding(2.0),
		Confidence: 0.8,
		Timestamp:  now,
	})

	unmatched, err := db.ListUnmatchedFaces(10)
	if err != nil {
		t.Fatalf("ListUnmatchedFaces: %v", err)
	}
	if len(unmatched) != 1 {
		t.Fatalf("ListUnmatchedFaces returned %d, want 1", len(unmatched))
	}
	if unmatched[0].Camera != "cam2" {
		t.Errorf("unmatched face camera = %q, want cam2", unmatched[0].Camera)
	}
}

func TestUpdateFacePerson(t *testing.T) {
	db := newTestDB(t)

	personID, _ := db.SavePerson("Alice", false, nil)
	now := time.Now().UTC()

	faceID, _ := db.SaveFace(Face{
		Camera:     "cam1",
		Embedding:  makeEmbedding(1.0),
		Confidence: 0.9,
		Timestamp:  now,
	})

	err := db.UpdateFacePerson(faceID, personID, 0.92)
	if err != nil {
		t.Fatalf("UpdateFacePerson: %v", err)
	}

	// Verify by listing person's faces
	faces, _ := db.ListFacesByPerson(personID, 10)
	if len(faces) != 1 {
		t.Fatalf("after update: ListFacesByPerson returned %d, want 1", len(faces))
	}
	if faces[0].Similarity == nil || *faces[0].Similarity != 0.92 {
		t.Errorf("updated similarity = %v, want 0.92", faces[0].Similarity)
	}
}

func TestForeignKey_DeletePersonNullsFaces(t *testing.T) {
	db := newTestDB(t)

	personID, _ := db.SavePerson("Alice", false, nil)
	now := time.Now().UTC()

	db.SaveFace(Face{
		Camera:     "cam1",
		PersonID:   &personID,
		Embedding:  makeEmbedding(1.0),
		Confidence: 0.9,
		Timestamp:  now,
	})

	// Delete the person
	_, err := db.db.Exec("DELETE FROM people WHERE id = ?", personID)
	if err != nil {
		t.Fatalf("delete person: %v", err)
	}

	// Face should still exist but with NULL person_id
	unmatched, _ := db.ListUnmatchedFaces(10)
	if len(unmatched) != 1 {
		t.Fatalf("after person delete: unmatched = %d, want 1", len(unmatched))
	}
}
