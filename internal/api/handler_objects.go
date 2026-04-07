package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/storage"
)

func (s *Server) ListObjects(w http.ResponseWriter, _ *http.Request) {
	objects, err := s.db.ListKnownObjects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if objects == nil {
		objects = []storage.KnownObject{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    objects,
		"total":    len(objects),
		"limit":    len(objects),
		"offset":   0,
		"has_more": false,
	})
}

func (s *Server) CreateObject(w http.ResponseWriter, r *http.Request) {
	if s.objectEmbedder == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object re-identification not available"})
		return
	}

	var req struct {
		EventID string `json:"event_id"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.EventID == "" || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event_id and name are required"})
		return
	}

	event, err := s.db.GetEventByID(req.EventID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if event == nil || !event.SnapshotAvailable {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event snapshot not found"})
		return
	}

	img, err := loadSnapshotImage(event.SnapshotPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load snapshot"})
		return
	}

	embedding, err := s.objectEmbedder.Embed(img, event.Box)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "embedding failed: " + err.Error()})
		return
	}

	obj := storage.KnownObject{
		Name:     req.Name,
		Label:    event.Label,
		Centroid: detect.Float32ToBytes(embedding),
	}
	objID, err := s.db.SaveKnownObject(obj)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cropDir := filepath.Join(s.snapshotPath, "objects")
	cropPath := s.objectEmbedder.SaveCrop(img, event.Box, cropDir, objID)
	if cropPath != "" {
		_ = s.db.UpdateKnownObjectCrop(objID, cropPath)
	}

	// Save as first reference
	s.db.SaveObjectReference(storage.ObjectReference{
		ObjectID:  objID,
		EventID:   req.EventID,
		Embedding: detect.Float32ToBytes(embedding),
		CropPath:  cropPath,
	})

	// Background re-match: tag recent events with this new object
	go s.rematchRecentEvents(objID)

	obj.ID = objID
	obj.CropPath = cropPath
	writeJSON(w, http.StatusCreated, obj)
}

func (s *Server) UpdateObject(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name           *string  `json:"name"`
		MatchThreshold *float64 `json:"match_threshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Name != nil && *req.Name != "" {
		if err := s.db.UpdateKnownObjectName(id, *req.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.MatchThreshold != nil {
		if err := s.db.UpdateKnownObjectThreshold(id, req.MatchThreshold); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) DismissSighting(w http.ResponseWriter, r *http.Request, id int64) {
	sighting, err := s.db.GetObjectSighting(id)
	if err != nil || sighting == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "sighting not found"})
		return
	}
	// Clear the sub_label and object_name on the event
	if sighting.EventID != "" {
		_ = s.db.UpdateEventObjectName(sighting.EventID, "")
		_ = s.db.UpdateEventSubLabel(sighting.EventID, "")
	}
	if err := s.db.DeleteObjectSighting(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

func (s *Server) DeleteObject(w http.ResponseWriter, r *http.Request, id int64) {
	obj, err := s.db.GetKnownObject(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if obj == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "object not found"})
		return
	}

	if obj.CropPath != "" {
		os.Remove(obj.CropPath)
	}
	if err := s.db.DeleteKnownObject(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) ListObjectSightings(w http.ResponseWriter, r *http.Request, id int64, params ListObjectSightingsParams) {
	limit := 50
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
	}
	sightings, err := s.db.ListObjectSightings(id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if sightings == nil {
		sightings = []storage.ObjectSighting{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    sightings,
		"total":    len(sightings),
		"limit":    limit,
		"offset":   0,
		"has_more": false,
	})
}

func (s *Server) GetObjectCrop(w http.ResponseWriter, r *http.Request, id int64, params GetObjectCropParams) {
	// Serve specific reference crop if ref param is provided
	if params.Ref != nil {
		refs, err := s.db.ListObjectReferences(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		refID := *params.Ref
		for _, ref := range refs {
			if ref.ID == refID && ref.CropPath != "" {
				http.ServeFile(w, r, ref.CropPath)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "reference crop not found"})
		return
	}

	obj, err := s.db.GetKnownObject(id)
	if err != nil || obj == nil || obj.CropPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "crop not found"})
		return
	}
	http.ServeFile(w, r, obj.CropPath)
}

func (s *Server) ListObjectReferences(w http.ResponseWriter, r *http.Request, id int64) {
	refs, err := s.db.ListObjectReferences(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if refs == nil {
		refs = []storage.ObjectReference{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    refs,
		"total":    len(refs),
		"limit":    len(refs),
		"offset":   0,
		"has_more": false,
	})
}

func (s *Server) AddObjectReference(w http.ResponseWriter, r *http.Request, id int64) {
	if s.objectEmbedder == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object re-identification not available"})
		return
	}

	objectID := id

	obj, err := s.db.GetKnownObject(objectID)
	if err != nil || obj == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "object not found"})
		return
	}

	var req struct {
		EventID string `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.EventID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event_id is required"})
		return
	}

	event, err := s.db.GetEventByID(req.EventID)
	if err != nil || event == nil || !event.SnapshotAvailable {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event snapshot not found"})
		return
	}

	img, err := loadSnapshotImage(event.SnapshotPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load snapshot"})
		return
	}

	embedding, err := s.objectEmbedder.Embed(img, event.Box)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "embedding failed: " + err.Error()})
		return
	}

	cropDir := filepath.Join(s.snapshotPath, "objects")
	cropPath := s.objectEmbedder.SaveCrop(img, event.Box, cropDir, objectID)

	refID, err := s.db.SaveObjectReference(storage.ObjectReference{
		ObjectID:  objectID,
		EventID:   req.EventID,
		Embedding: detect.Float32ToBytes(embedding),
		CropPath:  cropPath,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Recompute centroid from all references
	s.recomputeObjectCentroid(objectID)

	// Background re-match: scan recent unmatched events for this object
	go s.rematchRecentEvents(objectID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        refID,
		"object_id": objectID,
		"crop_path": cropPath,
	})
}

func (s *Server) DeleteObjectReference(w http.ResponseWriter, r *http.Request, id int64) {
	if err := s.db.DeleteObjectReference(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) recomputeObjectCentroid(objectID int64) {
	refs, err := s.db.ListObjectReferences(objectID)
	if err != nil || len(refs) == 0 {
		return
	}

	var centroid []float32
	for _, ref := range refs {
		emb := detect.BytesToFloat32(ref.Embedding)
		if centroid == nil {
			centroid = make([]float32, len(emb))
		}
		for i := range emb {
			centroid[i] += emb[i]
		}
	}

	n := float32(len(refs))
	var norm float64
	for i := range centroid {
		centroid[i] /= n
		norm += float64(centroid[i]) * float64(centroid[i])
	}
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range centroid {
			centroid[i] *= invNorm
		}
	}

	_ = s.db.UpdateKnownObjectCentroid(objectID, detect.Float32ToBytes(centroid))
}

func (s *Server) rematchRecentEvents(objectID int64) {
	if s.objectEmbedder == nil {
		return
	}
	obj, err := s.db.GetKnownObject(objectID)
	if err != nil || obj == nil || len(obj.Centroid) == 0 {
		return
	}

	centroid := detect.BytesToFloat32(obj.Centroid)
	threshold := s.ObjectMatchThreshold
	if threshold <= 0 {
		threshold = 0.65
	}

	events, err := s.db.RecentUnmatchedEventsByLabel(obj.Label, 200)
	if err != nil {
		slog.Error("rematch: failed to query events", "error", err)
		return
	}

	var matched int
	for _, ev := range events {
		if !ev.SnapshotAvailable || ev.SnapshotPath == "" {
			continue
		}
		img, err := loadSnapshotImage(ev.SnapshotPath)
		if err != nil {
			continue
		}
		embedding, err := s.objectEmbedder.Embed(img, ev.Box)
		if err != nil {
			continue
		}
		sim := detect.CosineSimilarity(embedding, centroid)
		if sim >= threshold {
			s.db.SaveObjectSighting(storage.ObjectSighting{
				EventID:    ev.ID,
				Camera:     ev.CameraName,
				ObjectID:   objectID,
				Similarity: sim,
				Timestamp:  ev.Timestamp,
			})
			_ = s.db.UpdateEventObjectName(ev.ID, obj.Name)
			_ = s.db.UpdateEventSubLabel(ev.ID, obj.Name)
			matched++
		}
	}
	if matched > 0 {
		slog.Info("rematch: retroactively tagged events", "object", obj.Name, "matched", matched)
	}
}
