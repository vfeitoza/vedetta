package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/safepath"
)

func (s *Server) ListCameras(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	type cameraInfo struct {
		Name      string `json:"name"`
		Online    bool   `json:"online"`
		HasMotion bool   `json:"has_motion"`
		PTZ       bool   `json:"ptz"`
	}
	result := make([]cameraInfo, len(statuses))
	for i, st := range statuses {
		_, hasPTZ := s.ptzClients[st.Name]
		result[i] = cameraInfo{Name: st.Name, Online: st.Online, HasMotion: st.HasMotion, PTZ: hasPTZ}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    result,
		"total":    len(result),
		"limit":    len(result),
		"offset":   0,
		"has_more": false,
	})
}

func (s *Server) GetCamera(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}
	st := cam.Status()
	_, hasPTZ := s.ptzClients[name]
	zones, _ := s.db.ListZones(name)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            st.Name,
		"online":          st.Online,
		"has_motion":      st.HasMotion,
		"degraded":        st.Degraded,
		"degraded_reason": st.DegradedReason,
		"last_frame":      st.LastFrame,
		"ptz":             hasPTZ,
		"zone_count":      len(zones),
		"recording":       s.recorder != nil,
		"source_fps":      st.SourceFPS,
	})
}

func (s *Server) SendPTZCommand(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	ptzClient, ok := s.ptzClients[name]
	if !ok || !ptzClient.Available() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "camera does not support PTZ"})
		return
	}

	var req struct {
		Action    string `json:"action"`
		Direction string `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	var err error
	switch req.Action {
	case "stop":
		err = ptzClient.Stop()
	case "move":
		var pan, tilt float64
		switch req.Direction {
		case "up":
			tilt = 0.5
		case "down":
			tilt = -0.5
		case "left":
			pan = -0.5
		case "right":
			pan = 0.5
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid direction"})
			return
		}
		err = ptzClient.ContinuousMove(pan, tilt, 0)
	case "zoom":
		var zoom float64
		switch req.Direction {
		case "in":
			zoom = 0.5
		case "out":
			zoom = -0.5
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid zoom direction"})
			return
		}
		err = ptzClient.ContinuousMove(0, 0, zoom)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid action"})
		return
	}

	if err != nil {
		slog.Error("PTZ command failed", "camera", name, "action", req.Action, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "PTZ command failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) GetCameraSnapshot(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	// Disable HTTP caching unconditionally — snapshots are live data, and a
	// cached error response would otherwise stick around indefinitely.
	setSnapshotNoCacheHeaders(w)

	if !cam.IsOnline() {
		w.Header().Set("X-Vedetta-Camera-State", "offline")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "camera offline"})
		return
	}

	img := cam.LiveFrame()
	if img == nil {
		w.Header().Set("X-Vedetta-Camera-State", "warming-up")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no frame yet"})
		return
	}

	if ts := cam.LastFrameTime(); !ts.IsZero() {
		w.Header().Set("Last-Modified", ts.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("Content-Type", "image/jpeg")
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: 85}); err != nil {
		slog.Error("failed to encode snapshot", "error", err)
	}
}

// setSnapshotNoCacheHeaders writes the headers needed to keep browsers and
// intermediate proxies from caching live snapshot responses. Single-source so
// success and error paths cannot drift.
func setSnapshotNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (s *Server) GetCameraThumbnail(w http.ResponseWriter, r *http.Request, name string, params GetCameraThumbnailParams) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	t := params.T

	// Find the segment containing the timestamp
	segments, err := s.db.QuerySegments(name, t, t.Add(1*time.Second))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(segments) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording at this time"})
		return
	}

	seg := segments[0]
	offset := t.Sub(seg.StartTime)
	if offset < 0 {
		offset = 0
	}

	jpegData, err := media.ExtractThumbnail(seg.Path, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "thumbnail extraction failed"})
		slog.Error("thumbnail extraction failed", "camera", name, "path", seg.Path, "offset", offset, "error", err)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Write(jpegData)
}

func (s *Server) PressDoorbell(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	// Capture current snapshot
	img := cam.LastSnapshot()
	if img == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no snapshot available"})
		return
	}

	// Create doorbell event
	eventID := fmt.Sprintf("%s-doorbell-%d", name, time.Now().UnixMilli())
	ev := camera.Event{
		ID:                eventID,
		CameraName:        name,
		Label:             "doorbell",
		Score:             1.0,
		Box:               [4]int{0, 0, img.Bounds().Dx(), img.Bounds().Dy()},
		Timestamp:         time.Now(),
		SnapshotAvailable: false,
	}

	// Compute the primary snapshot destination.
	snapDir, err := safepath.Join(s.snapshotPath, name)
	snapPath := ""
	if err != nil {
		slog.Error("invalid doorbell snapshot path", "camera", name, "error", err)
	} else {
		snapPath, err = safepath.Join(snapDir, safepath.FileComponent(eventID)+".jpg")
		if err != nil {
			slog.Error("invalid doorbell snapshot path", "camera", name, "error", err)
			snapPath = ""
		}
	}

	// Persist the event row first so SaveEventSnapshot's UPDATE has a row to hit.
	if err := s.db.SaveEvent(ev); err != nil {
		slog.Error("failed to save doorbell event", "error", err)
	}

	// Face recognition runs against the in-memory image; it does not need
	// the snapshot file on disk.
	if s.faceRecognizer != nil {
		fullBox := [4]int{0, 0, img.Bounds().Dx(), img.Bounds().Dy()}
		results := s.faceRecognizer.DetectAndEmbed(img, fullBox, s.faceCropDir)
		if len(results) > 0 {
			personID, _ := s.matchFaceToPerson(results[0].Embedding)
			if personID > 0 {
				if p, err := s.db.GetPerson(personID); err == nil && p != nil && p.Name != "" {
					ev.SubLabel = p.Name
					_ = s.db.UpdateEventSubLabel(ev.ID, p.Name)
				}
			}
		}
	}

	// Save the snapshot via the locked recorder method.
	if snapPath != "" {
		rgba := toRGBA(img)
		resolved, err := s.recorder.SaveEventSnapshot(ev, rgba, snapPath)
		if err != nil {
			slog.Error("save doorbell snapshot failed", "event", ev.ID, "error", err)
		} else {
			ev.SnapshotPath = resolved
			ev.SnapshotAvailable = true
		}
	}

	// Publish snapshot + doorbell to MQTT using the on-disk JPEG if present.
	if s.mqttClient != nil {
		var jpegData []byte
		if ev.SnapshotPath != "" {
			jpegData, _ = os.ReadFile(ev.SnapshotPath)
		}
		if len(jpegData) > 0 {
			s.mqttClient.PublishSnapshot(name, "doorbell", jpegData)
		}
		s.mqttClient.PublishDoorbell(name, ev.SubLabel, jpegData)
	}

	slog.Info("doorbell pressed", "camera", name, "event", eventID, "person", ev.SubLabel)

	s.broadcastSSE("doorbell", map[string]string{
		"event_id": eventID,
		"camera":   name,
		"person":   ev.SubLabel,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"event_id": eventID,
		"camera":   name,
		"person":   ev.SubLabel,
	})
}

func (s *Server) ListZones(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	zones, err := s.db.ListZones(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if zones == nil {
		zones = []camera.Zone{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    zones,
		"total":    len(zones),
		"limit":    len(zones),
		"offset":   0,
		"has_more": false,
	})
}

func (s *Server) CreateZone(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	var payload struct {
		Name            string      `json:"name"`
		Points          [][]float64 `json:"points"`
		X1              *float64    `json:"x1"`
		Y1              *float64    `json:"y1"`
		X2              *float64    `json:"x2"`
		Y2              *float64    `json:"y2"`
		Labels          []string    `json:"labels"`
		TrackPresence   bool        `json:"track_presence"`
		FaceRecognition bool        `json:"face_recognition"`
		Enabled         *bool       `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	points := zonePayloadPoints(payload.Points, payload.X1, payload.Y1, payload.X2, payload.Y2)
	if payload.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if !validZonePoints(points) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid points: expected at least 3 polygon points in normalized 0.0-1.0 coordinates"})
		return
	}

	z := camera.Zone{
		Camera:          name,
		Name:            payload.Name,
		Points:          points,
		Labels:          payload.Labels,
		TrackPresence:   payload.TrackPresence,
		FaceRecognition: payload.FaceRecognition,
		Enabled:         payload.Enabled == nil || *payload.Enabled,
	}
	if err := s.db.SaveZone(z); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Reload zones into camera
	s.reloadCameraZones(name, cam)

	writeJSON(w, http.StatusCreated, z)
}

func (s *Server) UpdateZone(w http.ResponseWriter, r *http.Request, name string, zone string) {
	zoneName := zone
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	existing, err := s.db.GetZone(name, zoneName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "zone not found"})
		return
	}

	var patch struct {
		Points          [][]float64 `json:"points"`
		X1              *float64    `json:"x1"`
		Y1              *float64    `json:"y1"`
		X2              *float64    `json:"x2"`
		Y2              *float64    `json:"y2"`
		Labels          []string    `json:"labels"`
		TrackPresence   *bool       `json:"track_presence"`
		FaceRecognition *bool       `json:"face_recognition"`
		Enabled         *bool       `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Apply patch fields onto existing zone
	z := *existing
	if patch.Points != nil {
		z.Points = patch.Points
	} else if patch.X1 != nil || patch.Y1 != nil || patch.X2 != nil || patch.Y2 != nil {
		x1, y1, x2, y2 := zoneBoundsFromPoints(z.Points)
		if patch.X1 != nil {
			x1 = *patch.X1
		}
		if patch.Y1 != nil {
			y1 = *patch.Y1
		}
		if patch.X2 != nil {
			x2 = *patch.X2
		}
		if patch.Y2 != nil {
			y2 = *patch.Y2
		}
		z.Points = rectanglePoints(x1, y1, x2, y2)
	}
	if patch.Labels != nil {
		z.Labels = patch.Labels
	}
	if patch.TrackPresence != nil {
		z.TrackPresence = *patch.TrackPresence
	}
	if patch.FaceRecognition != nil {
		z.FaceRecognition = *patch.FaceRecognition
	}
	if patch.Enabled != nil {
		z.Enabled = *patch.Enabled
	}

	if !validZonePoints(z.Points) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid points: expected at least 3 polygon points in normalized 0.0-1.0 coordinates"})
		return
	}

	if err := s.db.SaveZone(z); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.reloadCameraZones(name, cam)

	writeJSON(w, http.StatusOK, z)
}

func (s *Server) DeleteZone(w http.ResponseWriter, r *http.Request, name string, zone string) {
	zoneName := zone
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	if err := s.db.DeleteZone(name, zoneName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.reloadCameraZones(name, cam)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) GetZonePresence(w http.ResponseWriter, r *http.Request, name string, zone string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	zoneRecord, err := s.db.GetZone(name, zone)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if zoneRecord == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "zone not found"})
		return
	}

	// Read from live in-memory presence tracker (authoritative source)
	tracker := cam.PresenceTracker()
	allPresence := tracker.AllPresence()

	var presence []camera.ZonePresence
	for key, zp := range allPresence {
		if key.ZoneID == zoneRecord.ID {
			presence = append(presence, zp)
		}
	}
	if presence == nil {
		presence = []camera.ZonePresence{}
	}

	writeJSON(w, http.StatusOK, presence)
}

func zonePayloadPoints(points [][]float64, x1, y1, x2, y2 *float64) [][]float64 {
	if len(points) > 0 {
		return points
	}
	if x1 == nil || y1 == nil || x2 == nil || y2 == nil {
		return nil
	}
	return rectanglePoints(*x1, *y1, *x2, *y2)
}

func rectanglePoints(x1, y1, x2, y2 float64) [][]float64 {
	return [][]float64{
		{x1, y1},
		{x2, y1},
		{x2, y2},
		{x1, y2},
	}
}

func validZonePoints(points [][]float64) bool {
	if len(points) < 3 {
		return false
	}
	for _, point := range points {
		if len(point) != 2 {
			return false
		}
		if point[0] < 0 || point[0] > 1 || point[1] < 0 || point[1] > 1 {
			return false
		}
	}
	return true
}

func zoneBoundsFromPoints(points [][]float64) (x1, y1, x2, y2 float64) {
	if len(points) == 0 {
		return 0, 0, 0, 0
	}
	x1, y1 = points[0][0], points[0][1]
	x2, y2 = x1, y1
	for _, point := range points[1:] {
		if len(point) != 2 {
			continue
		}
		if point[0] < x1 {
			x1 = point[0]
		}
		if point[0] > x2 {
			x2 = point[0]
		}
		if point[1] < y1 {
			y1 = point[1]
		}
		if point[1] > y2 {
			y2 = point[1]
		}
	}
	return x1, y1, x2, y2
}

// reloadCameraZones loads zones from DB and updates the camera's zone list.
func (s *Server) reloadCameraZones(name string, cam *camera.Camera) {
	zones, err := s.db.ListZones(name)
	if err != nil {
		slog.Error("failed to reload zones", "camera", name, "error", err)
		return
	}
	cam.SetZones(zones)
}

// toRGBA returns img as *image.RGBA, converting if necessary.
func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, img, b.Min, draw.Src)
	return out
}

// TriggerDoorbell programmatically triggers a doorbell event for a camera.
// Used by ONVIF event subscribers when a doorbell press is detected.
func (s *Server) TriggerDoorbell(cameraName string) {
	cam := s.cameras.GetCamera(cameraName)
	if cam == nil {
		return
	}
	img := cam.LastSnapshot()
	if img == nil {
		return
	}

	eventID := fmt.Sprintf("%s-doorbell-%d", cameraName, time.Now().UnixMilli())
	ev := camera.Event{
		ID:                eventID,
		CameraName:        cameraName,
		Label:             "doorbell",
		Score:             1.0,
		Box:               [4]int{0, 0, img.Bounds().Dx(), img.Bounds().Dy()},
		Timestamp:         time.Now(),
		SnapshotAvailable: true,
	}

	snapDir, err := safepath.Join(s.snapshotPath, cameraName)
	snapPath := ""
	if err != nil {
		slog.Error("invalid doorbell snapshot path", "camera", cameraName, "error", err)
	} else {
		snapPath, err = safepath.Join(snapDir, safepath.FileComponent(eventID)+".jpg")
	}
	if err == nil && os.MkdirAll(snapDir, 0o755) == nil {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err == nil {
			if err := os.WriteFile(snapPath, buf.Bytes(), 0o644); err == nil {
				ev.SnapshotPath = snapPath
				if s.mqttClient != nil {
					s.mqttClient.PublishSnapshot(cameraName, "doorbell", buf.Bytes())
					s.mqttClient.PublishDoorbell(cameraName, "", buf.Bytes())
				}
			}
		}
	}

	// Face recognition
	if s.faceRecognizer != nil {
		fullBox := [4]int{0, 0, img.Bounds().Dx(), img.Bounds().Dy()}
		results := s.faceRecognizer.DetectAndEmbed(img, fullBox, s.faceCropDir)
		if len(results) > 0 {
			personID, _ := s.matchFaceToPerson(results[0].Embedding)
			if personID > 0 {
				if p, err := s.db.GetPerson(personID); err == nil && p != nil && p.Name != "" {
					ev.SubLabel = p.Name
					// Re-publish doorbell with person name
					if s.mqttClient != nil {
						var jpegData []byte
						if ev.SnapshotPath != "" {
							jpegData, _ = os.ReadFile(ev.SnapshotPath)
						}
						s.mqttClient.PublishDoorbell(cameraName, p.Name, jpegData)
					}
				}
			}
		}
	}

	if err := s.db.SaveEvent(ev); err != nil {
		slog.Error("failed to save doorbell event", "error", err)
	}
	if ev.SnapshotPath != "" {
		_ = s.db.UpdateEventSnapshotPath(ev.ID, ev.SnapshotPath)
	}
	if ev.SubLabel != "" {
		_ = s.db.UpdateEventSubLabel(ev.ID, ev.SubLabel)
	}

	slog.Info("doorbell event created", "camera", cameraName, "event", eventID, "person", ev.SubLabel)

	s.broadcastSSE("doorbell", map[string]string{
		"event_id": eventID,
		"camera":   cameraName,
		"person":   ev.SubLabel,
	})
}
