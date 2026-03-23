package api

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	"image/draw"
	"image/jpeg"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/recording"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/storage"
	"github.com/rvben/vedetta/internal/stream"

	"github.com/pion/webrtc/v4"
)

//go:embed static/*
var staticFiles embed.FS

var startTime = time.Now()

type Server struct {
	config         config.APIConfig
	auth           *auth.Checker
	db             *storage.DB
	cameras        *camera.Manager
	recorder       *recording.Recorder
	hub            *rtsp.Hub
	streams        *stream.StreamManager
	mse            *stream.MSEManager
	faceRecognizer *detect.FaceRecognizer
	objectEmbedder *detect.ObjectEmbedder
	snapshotPath   string
	faceCropDir    string
	cameraConfigs  []config.CameraConfig
	httpSrv        *http.Server
	mux            *http.ServeMux
	funcMap        template.FuncMap
	ready          atomic.Bool
}

func New(cfg config.APIConfig, authChecker *auth.Checker, db *storage.DB) *Server {
	s := &Server{
		config: cfg,
		auth:   authChecker,
		db:     db,
		mux:    http.NewServeMux(),
	}

	s.funcMap = template.FuncMap{
		"timeAgo": func(t time.Time) string {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return fmt.Sprintf("%ds ago", int(d.Seconds()))
			case d < time.Hour:
				return fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 24*time.Hour:
				return fmt.Sprintf("%dh ago", int(d.Hours()))
			default:
				return fmt.Sprintf("%dd ago", int(d.Hours()/24))
			}
		},
		"scorePercent": func(s float32) string {
			return fmt.Sprintf("%.0f%%", s*100)
		},
		"toFloat32": func(f float64) float32 { return float32(f) },
		"formatTime": func(t time.Time) template.HTML {
			iso := t.UTC().Format(time.RFC3339)
			display := t.UTC().Format("2006-01-02 15:04:05 UTC")
			return template.HTML(fmt.Sprintf(`<time datetime="%s">%s</time>`, iso, display))
		},
		"formatBytes": formatBytes,
		"displayName": displayName,
		"eventDuration": func(e camera.Event) string {
			if e.EndTime.IsZero() {
				return ""
			}
			d := e.EndTime.Sub(e.Timestamp)
			if d < time.Second {
				return ""
			}
			if d < time.Minute {
				return fmt.Sprintf("%ds", int(d.Seconds()))
			}
			return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
		},
	}

	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/auth/me", s.handleAuthMe)
	s.mux.HandleFunc("POST /api/tokens", s.handleCreateToken)
	s.mux.HandleFunc("DELETE /api/tokens/{id}", s.handleDeleteToken)

	// API endpoints
	s.mux.HandleFunc("GET /api/cameras", s.handleListCameras)
	s.mux.HandleFunc("GET /api/cameras/{name}/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("GET /api/events", s.handleListEvents)
	s.mux.HandleFunc("GET /api/events/{id}", s.handleGetEvent)
	s.mux.HandleFunc("GET /api/events/{id}/snapshot", s.handleEventSnapshot)
	s.mux.HandleFunc("GET /api/events/{id}/clip", s.handleEventClip)
	s.mux.HandleFunc("POST /api/events/{id}/clip", s.handleReextractClip)
	s.mux.HandleFunc("GET /api/events/counts", s.handleEventCounts)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/health/live", s.handleHealthLive)
	s.mux.HandleFunc("GET /api/health/ready", s.handleHealthReady)
	s.mux.HandleFunc("GET /api/system", s.handleSystemAPI)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	s.mux.HandleFunc("GET /api/recordings/calendar", s.handleRecordingsCalendar)
	s.mux.HandleFunc("GET /api/recordings/summary", s.handleRecordingsSummary)

	s.mux.HandleFunc("GET /api/cameras/{name}/timeline", s.handleCameraTimeline)
	s.mux.HandleFunc("GET /api/cameras/{name}/playback", s.handlePlayback)
	s.mux.HandleFunc("GET /api/cameras/{name}/thumbnail", s.handleThumbnail)
	s.mux.HandleFunc("GET /api/recordings/segments/{camera}", s.handleListSegments)
	s.mux.HandleFunc("GET /api/recordings/export/{camera}", s.handleRecordingExport)

	// Zone endpoints
	s.mux.HandleFunc("GET /api/cameras/{name}/zones/snapshot", s.handleSnapshot) // reuse camera snapshot for zone overlay background
	s.mux.HandleFunc("GET /api/cameras/{name}/zones", s.handleListZones)
	s.mux.HandleFunc("POST /api/cameras/{name}/zones", s.handleCreateZone)
	s.mux.HandleFunc("PUT /api/cameras/{name}/zones/{zone}", s.handleUpdateZone)
	s.mux.HandleFunc("DELETE /api/cameras/{name}/zones/{zone}", s.handleDeleteZone)
	s.mux.HandleFunc("GET /api/cameras/{name}/zones/{zone}/presence", s.handleZonePresence)

	// People/Face endpoints
	s.mux.HandleFunc("GET /api/people", s.handleListPeople)
	s.mux.HandleFunc("GET /api/people/{id}", s.handleGetPerson)
	s.mux.HandleFunc("PUT /api/people/{id}", s.handleUpdatePerson)
	s.mux.HandleFunc("DELETE /api/people/{id}", s.handleDeletePerson)
	s.mux.HandleFunc("GET /api/people/{id}/faces", s.handleListPersonFaces)
	s.mux.HandleFunc("GET /api/faces/unmatched", s.handleListUnmatchedFaces)
	s.mux.HandleFunc("PUT /api/faces/{id}/assign", s.handleAssignFace)
	s.mux.HandleFunc("GET /api/faces/{id}/crop", s.handleFaceCrop)
	s.mux.HandleFunc("POST /api/faces/backfill", s.handleFaceBackfill)
	s.mux.HandleFunc("POST /api/people/merge", s.handleMergePeople)

	// Object re-identification
	s.mux.HandleFunc("GET /api/objects", s.handleListObjects)
	s.mux.HandleFunc("POST /api/objects", s.handleCreateObject)
	s.mux.HandleFunc("DELETE /api/objects/{id}", s.handleDeleteObject)
	s.mux.HandleFunc("GET /api/objects/{id}/sightings", s.handleObjectSightings)
	s.mux.HandleFunc("GET /api/objects/{id}/crop", s.handleObjectCrop)
	s.mux.HandleFunc("POST /api/events/{id}/identify", s.handleIdentifyEvent)

	// Streaming endpoints
	s.mux.HandleFunc("POST /api/cameras/{name}/webrtc/offer", s.handleWebRTCOffer)
	s.mux.HandleFunc("GET /api/cameras/{name}/mse/ws", s.handleMSEWebSocket)
	s.mux.HandleFunc("GET /api/cameras/{name}/mjpeg", s.handleMJPEG)

	// HTML partial endpoints for htmx
	s.mux.HandleFunc("GET /partials/camera-grid", s.handleCameraGridPartial)
	s.mux.HandleFunc("GET /partials/dashboard-stats", s.handleDashboardStatsPartial)
	s.mux.HandleFunc("GET /partials/events-gallery", s.handleEventsGalleryPartial)
	s.mux.HandleFunc("GET /partials/event/{id}", s.handleEventDetailPartial)
	s.mux.HandleFunc("GET /partials/system-status", s.handleSystemStatusPartial)
	s.mux.HandleFunc("GET /partials/system", s.handleSystemPartial)

	// Serve static files at root
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		slog.Error("failed to create static sub filesystem", "error", err)
	} else {
		s.mux.Handle("GET /", http.FileServer(http.FS(staticSub)))
	}

	return s
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	handler := s.readyMiddleware(authMiddleware(s, s.mux))

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if s.config.TLSCert != "" && s.config.TLSKey != "" {
		s.httpSrv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		slog.Info("API server listening (HTTPS)", "addr", addr)
		return s.httpSrv.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
	}

	slog.Info("API server listening", "addr", addr)
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// SetSubsystems wires in the heavy dependencies once they're initialized.
// After calling this, camera/recording/streaming endpoints become functional.
func (s *Server) SetSubsystems(cameras *camera.Manager, recorder *recording.Recorder, hub *rtsp.Hub, faceRecognizer *detect.FaceRecognizer, objectEmbedder *detect.ObjectEmbedder, snapshotPath string, faceCropDir string, cameraConfigs []config.CameraConfig) {
	s.cameras = cameras
	s.recorder = recorder
	s.hub = hub
	s.streams = stream.NewStreamManager(hub)
	s.mse = stream.NewMSEManager(hub)
	s.faceRecognizer = faceRecognizer
	s.objectEmbedder = objectEmbedder
	s.snapshotPath = snapshotPath
	s.faceCropDir = faceCropDir
	s.cameraConfigs = cameraConfigs
	s.ready.Store(true)
	slog.Info("API server ready (all subsystems initialized)")
}

// readyMiddleware serves static files immediately but returns 503 for API/partial
// endpoints until subsystems are initialized.
func (s *Server) readyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ready.Load() && (strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/partials/")) {
			// Return JSON for API, HTML for partials
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"status":"starting","message":"Vedetta is initializing..."}`))
			} else {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`<div class="empty-state"><p>Vedetta is starting up...</p></div>`))
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Helper functions ---

func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func displayName(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// cameraStatuses returns the status of all cameras.
func (s *Server) cameraStatuses() []camera.CameraStatus {
	if s.cameras == nil {
		return nil
	}
	names := s.cameras.ListCameras()
	statuses := make([]camera.CameraStatus, 0, len(names))
	for _, name := range names {
		cam := s.cameras.GetCamera(name)
		if cam == nil {
			continue
		}
		statuses = append(statuses, cam.Status())
	}
	return statuses
}

// --- JSON API handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	status := "ok"

	// Database check — read-only, should be fast even under load
	dbStatus := "ok"
	if err := s.db.Ping(); err != nil {
		dbStatus = "error"
		status = "degraded"
	}

	// Camera check — in-memory, never blocks
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	// Storage check — from background-refreshed cache, never blocks
	storageStats := s.recorder.StorageStats()

	if storageStats.DiskLow {
		status = "degraded"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": status,
		"checks": map[string]any{
			"database": dbStatus,
			"cameras": map[string]any{
				"total":  len(statuses),
				"online": onlineCount,
			},
			"storage": map[string]any{
				"used_bytes":       storageStats.TotalBytes,
				"used":             formatBytes(storageStats.TotalBytes),
				"disk_available":   formatBytes(int64(storageStats.DiskAvailable)),
				"disk_low":         storageStats.DiskLow,
				"recording_paused": storageStats.RecordingPaused,
			},
		},
		"version": "0.1.0",
		"uptime":  formatDuration(time.Since(startTime)),
	})
}

func (s *Server) handleListCameras(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	type cameraInfo struct {
		Name      string `json:"name"`
		Online    bool   `json:"online"`
		HasMotion bool   `json:"has_motion"`
	}
	result := make([]cameraInfo, len(statuses))
	for i, st := range statuses {
		result[i] = cameraInfo{Name: st.Name, Online: st.Online, HasMotion: st.HasMotion}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	img := cam.LastSnapshot()
	if img == nil {
		// Return a 1x1 dark pixel so <img> tags don't break
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache")
		placeholder := image.NewRGBA(image.Rect(0, 0, 1, 1))
		_ = jpeg.Encode(w, placeholder, &jpeg.Options{Quality: 50})
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: 85}); err != nil {
		slog.Error("failed to encode snapshot", "error", err)
	}
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	cameraFilter := r.URL.Query().Get("camera")
	labelFilter := r.URL.Query().Get("label")
	zoneFilter := r.URL.Query().Get("zone")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	events, err := s.db.QueryEventsFiltered(cameraFilter, labelFilter, zoneFilter, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.db.GetEventByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if event == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleEventSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.db.GetEventByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if event == nil || event.SnapshotPath == "" || !event.SnapshotAvailable {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "snapshot not found"})
		return
	}
	filename := fmt.Sprintf("%s_%s.jpg", event.ID, event.Label)
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	}
	http.ServeFile(w, r, event.SnapshotPath)
}

func (s *Server) handleEventClip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.db.GetEventByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if event == nil || event.ClipPath == "" || !event.ClipAvailable {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "clip not found"})
		return
	}
	filename := fmt.Sprintf("%s_%s.mp4", event.ID, event.Label)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, event.ClipPath)
}

func (s *Server) handleReextractClip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.db.GetEventByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if event == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
		return
	}

	// Remove old clip if it exists
	if event.ClipPath != "" {
		os.Remove(event.ClipPath)
		_ = s.db.UpdateEventClipAvailability(event.ID, false)
	}

	if err := s.recorder.SaveClip(r.Context(), *event); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "event": id})
}

func (s *Server) handleSystemAPI(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	stats := s.recorder.StorageStats()

	writeJSON(w, http.StatusOK, map[string]any{
		"version":       "0.1.0",
		"uptime":        time.Since(startTime).String(),
		"decoder":       "native Go",
		"cameras":       len(statuses),
		"online":        onlineCount,
		"storage_bytes": stats.TotalBytes,
		"storage":       formatBytes(stats.TotalBytes),
	})
}

func (s *Server) handleCameraTimeline(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	date := time.Now().UTC()
	if dateStr := r.URL.Query().Get("date"); dateStr != "" {
		if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
			date = parsed
		}
	}

	segments, err := s.db.GetSegmentsForDate(name, date)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.db.QueryEventsForDate(name, date)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type timelineSegment struct {
		StartTime time.Time `json:"start_time"`
		EndTime   time.Time `json:"end_time"`
	}

	type timelineEvent struct {
		ID        string    `json:"id"`
		Label     string    `json:"label"`
		Score     float32   `json:"score"`
		Timestamp time.Time `json:"timestamp"`
	}

	segs := make([]timelineSegment, 0, len(segments))
	for _, seg := range segments {
		segs = append(segs, timelineSegment{
			StartTime: seg.StartTime,
			EndTime:   seg.EndTime,
		})
	}

	evts := make([]timelineEvent, 0, len(events))
	for _, evt := range events {
		evts = append(evts, timelineEvent{
			ID:        evt.ID,
			Label:     evt.Label,
			Score:     evt.Score,
			Timestamp: evt.Timestamp,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"segments": segs,
		"events":   evts,
	})
}

func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	startStr := r.URL.Query().Get("start")
	if startStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start parameter required"})
		return
	}

	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid start time format, use RFC3339"})
		return
	}

	// Find the segment that contains the requested timestamp
	segments, err := s.db.QuerySegments(name, start, start.Add(1*time.Second))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if len(segments) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no recording found for this timestamp"})
		return
	}

	seg := segments[0]

	// Calculate the offset into the segment
	offset := start.Sub(seg.StartTime)
	if offset < 0 {
		offset = 0
	}

	w.Header().Set("X-Segment-Start", seg.StartTime.Format(time.RFC3339))
	w.Header().Set("X-Segment-End", seg.EndTime.Format(time.RFC3339))

	// For HEAD requests, just confirm the segment exists
	if r.Method == "HEAD" {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Serve full segment if offset is negligible (< 2s)
	if offset < 2*time.Second {
		http.ServeFile(w, r, seg.Path)
		return
	}

	// Stream trimmed fMP4 starting at the requested offset
	w.Header().Set("Content-Type", "video/mp4")
	if err := media.TrimMP4ToWriter(seg.Path, w, offset); err != nil {
		slog.Error("playback trim failed", "path", seg.Path, "offset", offset, "error", err)
	}
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	startStr := r.URL.Query().Get("t")
	if startStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "t parameter required (RFC3339)"})
		return
	}

	t, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid time format"})
		return
	}

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

func (s *Server) handleListSegments(w http.ResponseWriter, r *http.Request) {
	cameraName := r.PathValue("camera")
	cam := s.cameras.GetCamera(cameraName)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	date := time.Now().UTC()
	if dateStr := r.URL.Query().Get("date"); dateStr != "" {
		if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
			date = parsed
		}
	}

	segments, err := s.db.GetSegmentsForDate(cameraName, date)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type segmentInfo struct {
		StartTime time.Time `json:"start_time"`
		EndTime   time.Time `json:"end_time"`
		SizeBytes int64     `json:"size_bytes"`
	}

	result := make([]segmentInfo, 0, len(segments))
	for _, seg := range segments {
		result = append(result, segmentInfo{
			StartTime: seg.StartTime,
			EndTime:   seg.EndTime,
			SizeBytes: seg.SizeBytes,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"segments": result})
}

func (s *Server) handleRecordingExport(w http.ResponseWriter, r *http.Request) {
	cameraName := r.PathValue("camera")

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	if startStr == "" || endStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start and end parameters required (RFC3339)"})
		return
	}

	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid start time, use RFC3339"})
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid end time, use RFC3339"})
		return
	}

	if !end.After(start) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "end must be after start"})
		return
	}

	if end.Sub(start) > 24*time.Hour {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "export range limited to 24 hours"})
		return
	}

	// Run PrepareExport with a timeout to prevent the handler from blocking
	// indefinitely on filesystem issues (e.g., EINTR on macOS APFS USB volumes).
	type exportResult struct {
		result *recording.ExportResult
		err    error
	}
	exportCh := make(chan exportResult, 1)
	go func() {
		res, err := s.recorder.PrepareExport(cameraName, start, end)
		exportCh <- exportResult{res, err}
	}()

	exportTimeout := 5 * time.Minute
	select {
	case res := <-exportCh:
		if res.err != nil {
			slog.Error("recording export failed",
				"camera", cameraName,
				"start", start.Format(time.RFC3339),
				"end", end.Format(time.RFC3339),
				"error", res.err,
			)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": res.err.Error()})
			return
		}
		defer res.result.Close()

		filename := fmt.Sprintf("%s_%s_%s.mp4",
			cameraName,
			start.Format("2006-01-02_15-04-05"),
			end.Format("15-04-05"),
		)

		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		// ServeContent handles Content-Type, Content-Length, Range requests,
		// and uses sendfile(2) for zero-copy streaming when possible.
		http.ServeContent(w, r, filename, time.Now(), res.result.File)

	case <-time.After(exportTimeout):
		slog.Error("recording export timed out",
			"camera", cameraName,
			"start", start.Format(time.RFC3339),
			"end", end.Format(time.RFC3339),
			"timeout", exportTimeout,
		)
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "export timed out"})

	case <-r.Context().Done():
		slog.Info("recording export cancelled by client",
			"camera", cameraName,
		)
	}
}

// --- Streaming handlers ---

func (s *Server) handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid SDP offer"})
		return
	}

	rtspURL := cam.RecordURL()

	answer, err := s.streams.HandleOffer(name, rtspURL, offer)
	if err != nil {
		slog.Error("WebRTC offer failed", "camera", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "WebRTC negotiation failed"})
		return
	}

	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) handleMSEWebSocket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	rtspURL := cam.RecordURL()
	s.mse.HandleWebSocket(w, r, name, rtspURL)
}

func (s *Server) handleMJPEG(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	handler := stream.MJPEGHandlerRGB24(cam.SnapshotRGB24, cam.FrameSize())
	handler.ServeHTTP(w, r)
}

// --- Zone handlers ---

func (s *Server) handleListZones(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
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
	writeJSON(w, http.StatusOK, zones)
}

func (s *Server) handleCreateZone(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
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

func (s *Server) handleUpdateZone(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	zoneName := r.PathValue("zone")
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

func (s *Server) handleDeleteZone(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	zoneName := r.PathValue("zone")
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

func (s *Server) handleZonePresence(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	zoneName := r.PathValue("zone")
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	zone, err := s.db.GetZone(name, zoneName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if zone == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "zone not found"})
		return
	}

	// Read from live in-memory presence tracker (authoritative source)
	tracker := cam.PresenceTracker()
	allPresence := tracker.AllPresence()

	var presence []camera.ZonePresence
	for key, zp := range allPresence {
		if key.ZoneID == zone.ID {
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

// --- HTML partial handlers for htmx ---

func (s *Server) handleCameraGridPartial(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()

	type cameraCard struct {
		Name        string
		DisplayName string
		Online      bool
		HasMotion   bool
	}

	cards := make([]cameraCard, 0, len(statuses))
	for _, st := range statuses {
		cards = append(cards, cameraCard{
			Name:        st.Name,
			DisplayName: displayName(st.Name),
			Online:      st.Online,
			HasMotion:   st.HasMotion,
		})
	}

	tmpl := template.Must(template.New("grid").Parse(`{{range .}}<div class="cam-card" onclick="location.href='/camera.html?name={{.Name}}'" role="listitem">
  <div class="cam-preview">
    <img src="/api/cameras/{{.Name}}/snapshot" alt="{{.Name}}" loading="lazy">
    <div class="cam-live-badge">
      <span class="cam-live-dot {{if .Online}}{{else}}offline{{end}}"></span>
      {{if .Online}}LIVE{{else}}OFFLINE{{end}}
    </div>
  </div>
  <div class="cam-footer">
    <span class="cam-name">{{.DisplayName}}</span>
  </div>
</div>{{end}}`))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, cards); err != nil {
		slog.Error("template error", "error", err)
	}
}

func (s *Server) handleDashboardStatsPartial(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	eventsToday, _ := s.db.CountEventsToday()
	stats := s.recorder.StorageStats()

	type dashData struct {
		CameraCount int
		OnlineCount int
		EventsToday int
		Storage     string
	}

	data := dashData{
		CameraCount: len(statuses),
		OnlineCount: onlineCount,
		EventsToday: eventsToday,
		Storage:     formatBytes(stats.TotalBytes),
	}

	tmpl := template.Must(template.New("stats").Parse(
		`<div class="stat-card"><div class="stat-label">Cameras</div><div class="stat-value">{{.CameraCount}}</div></div>` +
			`<div class="stat-card"><div class="stat-label">Online</div><div class="stat-value green">{{.OnlineCount}}</div></div>` +
			`<div class="stat-card"><div class="stat-label">Events Today</div><div class="stat-value">{{.EventsToday}}</div></div>` +
			`<div class="stat-card"><div class="stat-label">Storage</div><div class="stat-value">{{.Storage}}</div></div>`))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("template error", "error", err)
	}
}

func (s *Server) handleEventsGalleryPartial(w http.ResponseWriter, r *http.Request) {
	cameraFilter := r.URL.Query().Get("camera")
	labelFilter := r.URL.Query().Get("label")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	events, err := s.db.QueryEvents(cameraFilter, labelFilter, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")

	if offset == 0 && len(events) == 0 {
		_, _ = fmt.Fprint(w, `<div class="empty-state"><p>No events recorded yet.</p></div>`)
		return
	}

	tmpl := template.Must(template.New("gallery").Funcs(s.funcMap).Parse(
		`{{range .}}` +
			`<a class="event-card" href="/event.html?id={{.ID}}" role="listitem">` +
			`<div class="event-thumb">` +
			`{{if .SnapshotAvailable}}<img src="/api/events/{{.ID}}/snapshot" alt="{{.Label}}" loading="lazy">` +
			`{{else}}<img src="/api/cameras/{{.CameraName}}/snapshot" alt="{{.Label}}" loading="lazy">{{end}}` +
			`<span class="event-label-badge {{.Label}}">{{.Label}}</span>` +
			`<span class="event-score-badge">{{scorePercent .Score}}</span>` +
			`{{with eventDuration .}}<span class="event-duration-badge">{{.}}</span>{{end}}` +
			`</div>` +
			`<div class="event-card-footer">` +
			`<span class="event-camera-name">{{.CameraName}}</span>` +
			`<span class="event-time">{{timeAgo .Timestamp}}</span>` +
			`</div>` +
			`</a>{{end}}`))

	if err := tmpl.Execute(w, events); err != nil {
		slog.Error("template error", "error", err)
	}

	// If we got a full page of results, append a sentinel for infinite scroll
	if len(events) == limit {
		nextOffset := offset + limit
		nextURL := fmt.Sprintf("/partials/events-gallery?limit=%d&offset=%d", limit, nextOffset)
		if cameraFilter != "" {
			nextURL += "&camera=" + cameraFilter
		}
		if labelFilter != "" {
			nextURL += "&label=" + labelFilter
		}
		_, _ = fmt.Fprintf(w, `<div id="load-more-trigger" hx-get="%s" hx-trigger="revealed" hx-swap="outerHTML"></div>`, nextURL)
	}
}

func (s *Server) handleEventCounts(w http.ResponseWriter, _ *http.Request) {
	total, _ := s.db.CountEvents()
	byLabel, _ := s.db.CountEventsByLabel()
	byCamera, _ := s.db.CountEventsByCamera()

	writeJSON(w, http.StatusOK, map[string]any{
		"total":     total,
		"by_label":  byLabel,
		"by_camera": byCamera,
	})
}

func (s *Server) handleEventDetailPartial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.db.GetEventByID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if event == nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	prevID, nextID, _ := s.db.GetAdjacentEvents(id)

	sightings, _ := s.db.GetEventSightings(id)

	type eventDetailData struct {
		camera.Event
		PrevID       string
		NextID       string
		RecordingURL string
		HasRecording bool
		Duration     string
		Sightings    []storage.ObjectSighting
	}

	recURL := fmt.Sprintf("/camera.html?name=%s&t=%s",
		event.CameraName,
		event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	)

	var duration string
	if !event.EndTime.IsZero() {
		d := event.EndTime.Sub(event.Timestamp).Round(time.Second)
		duration = d.String()
	}

	hasRecording := s.recorder.HasSegments(event.CameraName, event.Timestamp)

	data := eventDetailData{
		Event:        *event,
		PrevID:       prevID,
		NextID:       nextID,
		RecordingURL: recURL,
		HasRecording: hasRecording,
		Duration:     duration,
		Sightings:    sightings,
	}

	tmpl := template.Must(template.New("detail").Funcs(s.funcMap).Parse(
		`<div class="page-header"><h1>{{.Label}} Detection</h1></div>` +
			`<div class="event-detail-layout">` +
			`<div class="event-media">` +
			`{{if .SnapshotAvailable}}<img id="event-snapshot" src="/api/events/{{.ID}}/snapshot" alt="event snapshot">` +
			`{{else}}<img id="event-snapshot" src="/api/cameras/{{.CameraName}}/snapshot" alt="event">{{end}}` +
			`{{if .HasRecording}}<div class="play-overlay" id="play-overlay" onclick="playEventRecording(this, '{{.CameraName}}', '{{.Timestamp.Format "2006-01-02T15:04:05Z07:00"}}')">` +
			`<svg viewBox="0 0 24 24" fill="white" width="64" height="64"><polygon points="5 3 19 12 5 21 5 3"/></svg>` +
			`</div>{{else if .ClipAvailable}}<div class="play-overlay" id="play-overlay" onclick="playEventClip(this, '{{.ID}}')">` +
			`<svg viewBox="0 0 24 24" fill="white" width="64" height="64"><polygon points="5 3 19 12 5 21 5 3"/></svg>` +
			`</div>{{end}}` +
			`</div>` +
			`<div class="event-sidebar">` +
			`<div class="meta-card">` +
			`<div class="meta-card-header">Details</div>` +
			`<div class="meta-row"><span class="key">Camera</span><span class="val">{{.CameraName}}</span></div>` +
			`<div class="meta-row"><span class="key">Label</span><span class="val">{{.Label}}</span></div>` +
			`<div class="meta-row"><span class="key">Confidence</span><span class="val">{{scorePercent .Score}}</span></div>` +
			`<div class="meta-row"><span class="key">Time</span><span class="val">{{formatTime .Timestamp}}</span></div>` +
			`{{if .Duration}}<div class="meta-row"><span class="key">Duration</span><span class="val">{{.Duration}}</span></div>{{end}}` +
			`<div class="meta-row"><span class="key">Event ID</span><span class="val mono">{{.ID}}</span></div>` +
			`</div>` +
			`<div class="meta-card">` +
			`<div class="meta-card-header">Downloads</div>` +
			`{{if .ClipAvailable}}<a href="/api/events/{{.ID}}/clip" download class="download-row">` +
			`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>` +
			` Download Clip</a>{{end}}` +
			`{{if .SnapshotAvailable}}<a href="/api/events/{{.ID}}/snapshot?download=1" download class="download-row">` +
			`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>` +
			` Download Snapshot</a>{{end}}` +
			`{{if not .ClipAvailable}}{{if not .SnapshotAvailable}}<div class="download-row disabled">No media available</div>{{end}}{{end}}` +
			`{{if .HasRecording}}<a href="{{.RecordingURL}}" class="download-row">` +
			`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="5 3 19 12 5 21 5 3"/></svg>` +
			` View in Recording</a>{{end}}` +
			`</div>` +
			`{{if .Sightings}}<div class="meta-card">` +
			`<div class="meta-card-header">Recognized</div>` +
			`{{range .Sightings}}<div class="meta-row"><span class="key">{{.ObjectName}}</span><span class="val">{{scorePercent (toFloat32 .Similarity)}}</span></div>{{end}}` +
			`</div>{{end}}` +
			`{{if .SnapshotAvailable}}<div class="meta-card">` +
			`<button class="btn btn-sm" style="width:100%" onclick="trackObject('{{.ID}}', '{{.Label}}')">` +
			`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>` +
			` Track this {{.Label}}</button>` +
			`</div>{{end}}` +
			`<div class="event-nav">` +
			`{{if .PrevID}}<a href="/event.html?id={{.PrevID}}" class="btn" data-prev-id="{{.PrevID}}">&#8592; Previous</a>{{else}}<span class="btn" style="opacity:0.3;pointer-events:none">&#8592; Previous</span>{{end}}` +
			`{{if .NextID}}<a href="/event.html?id={{.NextID}}" class="btn" data-next-id="{{.NextID}}">Next &#8594;</a>{{else}}<span class="btn" style="opacity:0.3;pointer-events:none">Next &#8594;</span>{{end}}` +
			`</div>` +
			`</div>` +
			`</div>`))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("template error", "error", err)
	}
}

func (s *Server) handleSystemStatusPartial(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	type topnavData struct {
		Total  int
		Online int
	}

	data := topnavData{Total: len(statuses), Online: onlineCount}

	tmpl := template.Must(template.New("sysstatus").Parse(
		`<span class="topnav-stat"><span class="value">{{.Total}}</span> cameras</span>` +
			`<span class="topnav-stat"><span class="value green">{{.Online}}</span> online</span>`))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("template error", "error", err)
	}
}

func (s *Server) handleSystemPartial(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	decoderName := "native Go"

	uptime := time.Since(startTime)
	uptimeStr := formatDuration(uptime)

	stats := s.recorder.StorageStats()
	totalBytes := stats.TotalBytes

	type storageEntry struct {
		Camera  string
		Bytes   int64
		Display string
		Percent float64
	}

	storageEntries := make([]storageEntry, 0, len(stats.CameraStats))
	for cam, bytes := range stats.CameraStats {
		pct := float64(0)
		if totalBytes > 0 {
			pct = float64(bytes) / float64(totalBytes) * 100
		}
		storageEntries = append(storageEntries, storageEntry{
			Camera:  cam,
			Bytes:   bytes,
			Display: formatBytes(bytes),
			Percent: pct,
		})
	}

	type sysData struct {
		Version     string
		Uptime      string
		Decoder     string
		GoVersion   string
		CameraCount int
		OnlineCount int
		Statuses    []camera.CameraStatus
		TotalBytes  int64
		TotalStr    string
		SegCount    int
		Storage     []storageEntry
	}

	data := sysData{
		Version:     "0.1.0",
		Uptime:      uptimeStr,
		Decoder:     decoderName,
		GoVersion:   runtime.Version(),
		CameraCount: len(statuses),
		OnlineCount: onlineCount,
		Statuses:    statuses,
		TotalBytes:  totalBytes,
		TotalStr:    formatBytes(totalBytes),
		SegCount:    stats.SegmentCount,
		Storage:     storageEntries,
	}

	tmpl := template.Must(template.New("system").Funcs(s.funcMap).Parse(systemPartialTemplate))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("template error", "error", err)
	}
}

const systemPartialTemplate = `<div class="sys-card">
  <div class="sys-card-header">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
    System Info
  </div>
  <div class="sys-card-body">
    <div class="sys-row"><span class="key">Version</span><span class="val">{{.Version}}</span></div>
    <div class="sys-row"><span class="key">Uptime</span><span class="val">{{.Uptime}}</span></div>
    <div class="sys-row"><span class="key">Decoder</span><span class="val">{{.Decoder}}</span></div>
    <div class="sys-row"><span class="key">Go</span><span class="val">{{.GoVersion}}</span></div>
    <div class="sys-row"><span class="key">Cameras</span><span class="val">{{.CameraCount}} ({{.OnlineCount}} online)</span></div>
  </div>
</div>
<div class="sys-card">
  <div class="sys-card-header">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>
    Camera Status
  </div>
  <div class="sys-card-body">
    <table style="width:100%">
      <thead><tr><th style="text-align:left">Camera</th><th style="text-align:left">Status</th></tr></thead>
      <tbody>
      {{range .Statuses}}<tr>
        <td>{{displayName .Name}}</td>
        <td>{{if .Online}}<span class="green">Online</span>{{else}}<span class="red">Offline</span>{{end}}</td>
      </tr>{{end}}
      </tbody>
    </table>
  </div>
</div>
<div class="sys-card">
  <div class="sys-card-header">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>
    Storage
  </div>
  <div class="sys-card-body">
    <div class="sys-row"><span class="key">Total</span><span class="val">{{.TotalStr}}</span></div>
    <div class="sys-row"><span class="key">Segments</span><span class="val">{{.SegCount}}</span></div>
    {{range .Storage}}<div style="margin-top: 0.5rem">
      <div class="sys-row"><span class="key">{{displayName .Camera}}</span><span class="val">{{.Display}}</span></div>
      <div class="storage-bar"><div class="storage-bar-fill" style="width: {{printf "%.0f" .Percent}}%"></div></div>
    </div>{{end}}
  </div>
</div>`

func (s *Server) handleRecordingsCalendar(w http.ResponseWriter, r *http.Request) {
	cameraFilter := r.URL.Query().Get("camera")
	monthStr := r.URL.Query().Get("month")

	year, month := time.Now().Year(), int(time.Now().Month())
	if monthStr != "" {
		if parsed, err := time.Parse("2006-01", monthStr); err == nil {
			year = parsed.Year()
			month = int(parsed.Month())
		}
	}

	days, err := s.db.GetRecordingDays(cameraFilter, year, month)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if days == nil {
		days = []int{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}

func (s *Server) handleRecordingsSummary(w http.ResponseWriter, r *http.Request) {
	dateStr := r.URL.Query().Get("date")

	date := time.Now().UTC()
	if dateStr != "" {
		if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
			date = parsed
		}
	}

	// Get all segments for the date across all cameras.
	segments, err := s.db.GetSegmentsForDate("", date)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type segmentInfo struct {
		StartTime time.Time `json:"start_time"`
		EndTime   time.Time `json:"end_time"`
		SizeBytes int64     `json:"size_bytes"`
	}

	type cameraSummary struct {
		Name       string        `json:"name"`
		Segments   []segmentInfo `json:"segments"`
		TotalBytes int64         `json:"total_bytes"`
	}

	// Group by camera, preserving config order.
	cameraOrder := s.cameras.ListCameras()
	grouped := make(map[string]*cameraSummary, len(cameraOrder))
	for _, name := range cameraOrder {
		grouped[name] = &cameraSummary{Name: name, Segments: []segmentInfo{}}
	}

	var totalBytes int64
	for _, seg := range segments {
		cs, ok := grouped[seg.Camera]
		if !ok {
			cs = &cameraSummary{Name: seg.Camera, Segments: []segmentInfo{}}
			grouped[seg.Camera] = cs
			cameraOrder = append(cameraOrder, seg.Camera)
		}
		cs.Segments = append(cs.Segments, segmentInfo{
			StartTime: seg.StartTime,
			EndTime:   seg.EndTime,
			SizeBytes: seg.SizeBytes,
		})
		cs.TotalBytes += seg.SizeBytes
		totalBytes += seg.SizeBytes
	}

	// Build ordered result, skip cameras with no data.
	result := make([]cameraSummary, 0, len(cameraOrder))
	for _, name := range cameraOrder {
		cs := grouped[name]
		if len(cs.Segments) > 0 {
			result = append(result, *cs)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cameras":     result,
		"total_bytes": totalBytes,
	})
}

// formatDuration returns a human-readable duration string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// ─── People / Face Handlers ───

func (s *Server) handleListPeople(w http.ResponseWriter, _ *http.Request) {
	people, err := s.db.ListPeople()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type personResponse struct {
		ID        int64     `json:"id"`
		Name      string    `json:"name"`
		Ignore    bool      `json:"ignore"`
		FaceCount int       `json:"face_count"`
		CreatedAt time.Time `json:"created_at"`
	}

	resp := make([]personResponse, 0, len(people))
	for _, p := range people {
		faces, _ := s.db.ListFacesByPerson(p.ID, 0)
		resp = append(resp, personResponse{
			ID:        p.ID,
			Name:      p.Name,
			Ignore:    p.Ignore,
			FaceCount: len(faces),
			CreatedAt: p.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetPerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid person ID"})
		return
	}
	person, err := s.db.GetPerson(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if person == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "person not found"})
		return
	}
	faces, _ := s.db.ListFacesByPerson(id, 20)

	type faceResponse struct {
		ID         int64     `json:"id"`
		Camera     string    `json:"camera"`
		Confidence float64   `json:"confidence"`
		Similarity *float64  `json:"similarity,omitempty"`
		CropPath   string    `json:"crop_path"`
		Timestamp  time.Time `json:"timestamp"`
	}
	faceResp := make([]faceResponse, 0, len(faces))
	for _, f := range faces {
		faceResp = append(faceResp, faceResponse{
			ID:         f.ID,
			Camera:     f.Camera,
			Confidence: f.Confidence,
			Similarity: f.Similarity,
			CropPath:   f.CropPath,
			Timestamp:  f.Timestamp,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         person.ID,
		"name":       person.Name,
		"ignore":     person.Ignore,
		"face_count": len(faces),
		"created_at": person.CreatedAt,
		"faces":      faceResp,
	})
}

func (s *Server) handleUpdatePerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid person ID"})
		return
	}
	var req struct {
		Name   *string `json:"name"`
		Ignore *bool   `json:"ignore"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Name != nil {
		if err := s.db.UpdatePersonName(id, *req.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.Ignore != nil {
		if err := s.db.SetPersonIgnore(id, *req.Ignore); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid person ID"})
		return
	}
	if err := s.db.DeletePerson(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleMergePeople(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetID int64 `json:"target_id"`
		SourceID int64 `json:"source_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.TargetID == 0 || req.SourceID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_id and source_id are required"})
		return
	}
	if req.TargetID == req.SourceID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot merge a person with themselves"})
		return
	}

	if err := s.db.MergePeople(req.TargetID, req.SourceID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Recompute target person's centroid from all their faces
	faces, _ := s.db.ListFacesByPerson(req.TargetID, 0)
	if len(faces) > 0 {
		var centroid []float32
		for _, f := range faces {
			emb := detect.BytesToFloat32(f.Embedding)
			if centroid == nil {
				centroid = make([]float32, len(emb))
			}
			for i := range emb {
				centroid[i] += emb[i]
			}
		}
		n := float32(len(faces))
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
		_ = s.db.UpdatePersonCentroid(req.TargetID, detect.Float32ToBytes(centroid))
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

func (s *Server) handleListPersonFaces(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid person ID"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	faces, err := s.db.ListFacesByPerson(id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if faces == nil {
		faces = []storage.Face{}
	}
	writeJSON(w, http.StatusOK, faces)
}

func (s *Server) handleListUnmatchedFaces(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	faces, err := s.db.ListUnmatchedFaces(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if faces == nil {
		faces = []storage.Face{}
	}
	writeJSON(w, http.StatusOK, faces)
}

func (s *Server) handleAssignFace(w http.ResponseWriter, r *http.Request) {
	faceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid face ID"})
		return
	}
	var req struct {
		PersonID int64  `json:"person_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	personID := req.PersonID
	if personID == 0 {
		name := req.Name
		if name == "" {
			name = "Unknown"
		}
		newID, err := s.db.SavePerson(name, false, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		personID = newID
	}

	if err := s.db.UpdateFacePerson(faceID, personID, 1.0); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "assigned", "person_id": personID})
}

func (s *Server) handleFaceCrop(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid face ID"})
		return
	}
	cropPath, err := s.db.GetFaceCropPath(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if cropPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no crop available"})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, cropPath)
}

func (s *Server) handleFaceBackfill(w http.ResponseWriter, r *http.Request) {
	if s.faceRecognizer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "face recognition not available"})
		return
	}
	if s.snapshotPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no snapshot path configured"})
		return
	}

	// Get already-processed snapshot files to skip
	processedEvents, err := s.db.FaceEventIDs()
	if err != nil {
		slog.Warn("could not load processed face events", "error", err)
	}
	processed := make(map[string]bool, len(processedEvents))
	for _, eid := range processedEvents {
		processed[eid] = true
	}

	// Scan all snapshot JPEGs on disk (not just person events)
	type snapshotEntry struct {
		camera  string
		eventID string
		path    string
	}
	var snapshots []snapshotEntry
	camDirs, _ := os.ReadDir(s.snapshotPath)
	for _, camDir := range camDirs {
		if !camDir.IsDir() {
			continue
		}
		camName := camDir.Name()
		camPath := filepath.Join(s.snapshotPath, camName)
		files, _ := os.ReadDir(camPath)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jpg") {
				continue
			}
			eventID := strings.TrimSuffix(f.Name(), ".jpg")
			if processed[eventID] {
				continue
			}
			snapshots = append(snapshots, snapshotEntry{
				camera:  camName,
				eventID: eventID,
				path:    filepath.Join(camPath, f.Name()),
			})
		}
	}

	// Stream progress as JSON lines
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)

	var totalScanned, totalFaces, totalNewPeople, totalMatched, skipped int
	skipped = len(processedEvents)

	for _, snap := range snapshots {
		f, err := os.Open(snap.path)
		if err != nil {
			continue
		}

		img, err := jpeg.Decode(f)
		f.Close()
		if err != nil {
			continue
		}

		// Convert to RGBA
		rgba, ok := img.(*image.RGBA)
		if !ok {
			bounds := img.Bounds()
			rgba = image.NewRGBA(bounds)
			draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
		}

		// Use full image — let SCRFD find all faces
		bounds := rgba.Bounds()
		fullBox := [4]int{bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y}

		results := s.faceRecognizer.DetectAndEmbed(rgba, fullBox, s.faceCropDir)
		totalScanned++

		if len(results) > 0 {
			slog.Info("backfill: faces detected", "snapshot", snap.eventID, "count", len(results))
		}

		for _, result := range results {
			personID, similarity := s.matchFaceToPerson(result.Embedding)

			face := storage.Face{
				EventID:    snap.eventID,
				Camera:     snap.camera,
				Embedding:  detect.Float32ToBytes(result.Embedding),
				CropPath:   result.CropPath,
				Confidence: float64(result.Confidence),
				Timestamp:  time.Now(),
			}
			if personID > 0 {
				face.PersonID = &personID
				face.Similarity = &similarity
			}

			faceID, saveErr := s.db.SaveFace(face)
			if saveErr != nil {
				slog.Error("backfill: failed to save face", "error", saveErr)
				continue
			}

			totalFaces++

			if personID > 0 {
				s.updatePersonCentroid(personID, result.Embedding)
				totalMatched++
			} else {
				if s.clusterUnmatchedFace(faceID, result.Embedding) {
					totalNewPeople++
				}
			}
		}

		// Stream progress every 20 snapshots
		if totalScanned%20 == 0 && canFlush {
			progress := map[string]int{
				"scanned":    totalScanned,
				"faces":      totalFaces,
				"matched":    totalMatched,
				"new_people": totalNewPeople,
				"skipped":    skipped,
			}
			json.NewEncoder(w).Encode(progress)
			flusher.Flush()
		}
	}

	// Final result
	result := map[string]int{
		"scanned":    totalScanned,
		"faces":      totalFaces,
		"matched":    totalMatched,
		"new_people": totalNewPeople,
		"skipped":    skipped,
	}
	json.NewEncoder(w).Encode(result)
	if canFlush {
		flusher.Flush()
	}
}

// matchFaceToPerson finds the best matching person for a face embedding.
func (s *Server) matchFaceToPerson(embedding []float32) (int64, float64) {
	if s.faceRecognizer == nil {
		return 0, 0
	}
	people, err := s.db.ListPeople()
	if err != nil {
		return 0, 0
	}

	var bestID int64
	var bestSim float64
	threshold := s.faceRecognizer.MatchThreshold()

	for _, p := range people {
		if p.Ignore || len(p.Centroid) == 0 {
			continue
		}
		centroid := detect.BytesToFloat32(p.Centroid)
		sim := detect.CosineSimilarity(embedding, centroid)
		if sim > bestSim {
			bestSim = sim
			bestID = p.ID
		}
	}

	if bestSim >= threshold {
		return bestID, bestSim
	}
	return 0, 0
}

// updatePersonCentroid updates a person's centroid with a running average.
func (s *Server) updatePersonCentroid(personID int64, newEmbedding []float32) {
	p, err := s.db.GetPerson(personID)
	if err != nil || p == nil {
		return
	}

	if len(p.Centroid) == 0 {
		_ = s.db.UpdatePersonCentroid(personID, detect.Float32ToBytes(newEmbedding))
		return
	}

	old := detect.BytesToFloat32(p.Centroid)
	if len(old) != len(newEmbedding) {
		_ = s.db.UpdatePersonCentroid(personID, detect.Float32ToBytes(newEmbedding))
		return
	}

	alpha := float32(0.3)
	merged := make([]float32, len(old))
	var norm float64
	for i := range merged {
		merged[i] = (1-alpha)*old[i] + alpha*newEmbedding[i]
		norm += float64(merged[i]) * float64(merged[i])
	}
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range merged {
			merged[i] *= invNorm
		}
	}

	_ = s.db.UpdatePersonCentroid(personID, detect.Float32ToBytes(merged))
}

const clusterThreshold = 0.62

func (s *Server) clusterUnmatchedFace(newFaceID int64, embedding []float32) bool {
	unmatched, err := s.db.ListUnmatchedFaces(200)
	if err != nil || len(unmatched) == 0 {
		return false
	}

	var bestFace *storage.Face
	var bestSim float64
	for i := range unmatched {
		if unmatched[i].ID == newFaceID {
			continue
		}
		other := detect.BytesToFloat32(unmatched[i].Embedding)
		if len(other) == 0 {
			continue
		}
		sim := detect.CosineSimilarity(embedding, other)
		if sim > bestSim {
			bestSim = sim
			bestFace = &unmatched[i]
		}
	}

	if bestFace == nil || bestSim < clusterThreshold {
		return false
	}

	centroid := averageEmbeddings(embedding, detect.BytesToFloat32(bestFace.Embedding))
	personID, err := s.db.SavePerson("", false, detect.Float32ToBytes(centroid))
	if err != nil {
		return false
	}
	_ = s.db.UpdateFacePerson(bestFace.ID, personID, bestSim)
	_ = s.db.UpdateFacePerson(newFaceID, personID, 1.0)
	return true
}

func averageEmbeddings(a, b []float32) []float32 {
	if len(a) != len(b) {
		return a
	}
	out := make([]float32, len(a))
	var norm float64
	for i := range out {
		out[i] = (a[i] + b[i]) / 2
		norm += float64(out[i]) * float64(out[i])
	}
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range out {
			out[i] *= invNorm
		}
	}
	return out
}

// ─── Object Re-Identification Handlers ───

func (s *Server) handleListObjects(w http.ResponseWriter, _ *http.Request) {
	objects, err := s.db.ListKnownObjects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if objects == nil {
		objects = []storage.KnownObject{}
	}
	writeJSON(w, http.StatusOK, objects)
}

func (s *Server) handleCreateObject(w http.ResponseWriter, r *http.Request) {
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
	id, err := s.db.SaveKnownObject(obj)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cropDir := filepath.Join(s.snapshotPath, "objects")
	cropPath := s.objectEmbedder.SaveCrop(img, event.Box, cropDir, id)
	if cropPath != "" {
		_ = s.db.UpdateKnownObjectCrop(id, cropPath)
	}

	obj.ID = id
	obj.CropPath = cropPath
	writeJSON(w, http.StatusCreated, obj)
}

func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid object ID"})
		return
	}

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

func (s *Server) handleObjectSightings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid object ID"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	sightings, err := s.db.ListObjectSightings(id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if sightings == nil {
		sightings = []storage.ObjectSighting{}
	}
	writeJSON(w, http.StatusOK, sightings)
}

func (s *Server) handleObjectCrop(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid object ID"})
		return
	}
	obj, err := s.db.GetKnownObject(id)
	if err != nil || obj == nil || obj.CropPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "crop not found"})
		return
	}
	http.ServeFile(w, r, obj.CropPath)
}

func (s *Server) handleIdentifyEvent(w http.ResponseWriter, r *http.Request) {
	if s.objectEmbedder == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object re-identification not available"})
		return
	}

	eventID := r.PathValue("id")
	event, err := s.db.GetEventByID(eventID)
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

	knownObjects, err := s.db.ListKnownObjectsByLabel(event.Label)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	const matchThreshold = 0.65
	var matches []storage.ObjectSighting
	for _, obj := range knownObjects {
		centroid := detect.BytesToFloat32(obj.Centroid)
		if len(centroid) == 0 {
			continue
		}
		sim := detect.CosineSimilarity(embedding, centroid)
		if sim >= matchThreshold {
			sighting := storage.ObjectSighting{
				EventID:    eventID,
				Camera:     event.CameraName,
				ObjectID:   obj.ID,
				ObjectName: obj.Name,
				Similarity: sim,
				Timestamp:  event.Timestamp,
			}
			if _, err := s.db.SaveObjectSighting(sighting); err == nil {
				matches = append(matches, sighting)
			}
		}
	}

	if matches == nil {
		matches = []storage.ObjectSighting{}
	}
	writeJSON(w, http.StatusOK, matches)
}

func loadSnapshotImage(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, err
	}

	rgba, ok := img.(*image.RGBA)
	if !ok {
		bounds := img.Bounds()
		rgba = image.NewRGBA(bounds)
		draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	}
	return rgba, nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
