package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"image/jpeg"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rvben/watchpost/internal/camera"
	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/recording"
	"github.com/rvben/watchpost/internal/storage"
	"github.com/rvben/watchpost/internal/stream"

	"github.com/pion/webrtc/v4"
)

//go:embed static/*
var staticFiles embed.FS

var startTime = time.Now()

type Server struct {
	config   config.APIConfig
	db       *storage.DB
	cameras  *camera.Manager
	recorder *recording.Recorder
	streams  *stream.StreamManager
	mux      *http.ServeMux
	funcMap  template.FuncMap
}

func New(cfg config.APIConfig, db *storage.DB, cameras *camera.Manager, recorder *recording.Recorder) *Server {
	s := &Server{
		config:   cfg,
		db:       db,
		cameras:  cameras,
		recorder: recorder,
		streams:  stream.NewStreamManager(),
		mux:      http.NewServeMux(),
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
		"formatTime": func(t time.Time) string {
			return t.Format("2006-01-02 15:04:05")
		},
		"formatBytes": formatBytes,
		"displayName": displayName,
	}

	// API endpoints
	s.mux.HandleFunc("GET /api/cameras", s.handleListCameras)
	s.mux.HandleFunc("GET /api/cameras/{name}/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("GET /api/events", s.handleListEvents)
	s.mux.HandleFunc("GET /api/events/{id}", s.handleGetEvent)
	s.mux.HandleFunc("GET /api/events/{id}/snapshot", s.handleEventSnapshot)
	s.mux.HandleFunc("GET /api/events/{id}/clip", s.handleEventClip)
	s.mux.HandleFunc("GET /api/events/counts", s.handleEventCounts)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/system", s.handleSystemAPI)

	s.mux.HandleFunc("GET /api/recordings/calendar", s.handleRecordingsCalendar)

	s.mux.HandleFunc("GET /api/cameras/{name}/timeline", s.handleCameraTimeline)

	// Streaming endpoints
	s.mux.HandleFunc("POST /api/cameras/{name}/webrtc/offer", s.handleWebRTCOffer)
	s.mux.HandleFunc("GET /api/cameras/{name}/mjpeg", s.handleMJPEG)

	// HTML partial endpoints for htmx
	s.mux.HandleFunc("GET /partials/camera-grid", s.handleCameraGridPartial)
	s.mux.HandleFunc("GET /partials/dashboard-stats", s.handleDashboardStatsPartial)
	s.mux.HandleFunc("GET /partials/events-gallery", s.handleEventsGalleryPartial)
	s.mux.HandleFunc("GET /partials/event/{id}", s.handleEventDetailPartial)
	s.mux.HandleFunc("GET /partials/system-status", s.handleSystemStatusPartial)
	s.mux.HandleFunc("GET /partials/system", s.handleSystemPartial)
	s.mux.HandleFunc("GET /partials/recordings", s.handleRecordingsPartial)

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
	slog.Info("API server listening", "addr", addr)
	return http.ListenAndServe(addr, s.mux)
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
	names := s.cameras.ListCameras()
	statuses := make([]camera.CameraStatus, 0, len(names))
	for _, name := range names {
		cam := s.cameras.GetCamera(name)
		if cam == nil {
			continue
		}
		statuses = append(statuses, camera.CameraStatus{
			Name:   name,
			Online: cam.IsOnline(),
		})
	}
	return statuses
}

// --- JSON API handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListCameras(w http.ResponseWriter, _ *http.Request) {
	names := s.cameras.ListCameras()
	writeJSON(w, http.StatusOK, map[string]any{"cameras": names})
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
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no snapshot available"})
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
	if event == nil || event.SnapshotPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "snapshot not found"})
		return
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
	if event == nil || event.ClipPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "clip not found"})
		return
	}
	http.ServeFile(w, r, event.ClipPath)
}

func (s *Server) handleSystemAPI(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	hwaccelName := "none"
	if hw := s.cameras.HWAccelBackend(); hw != nil {
		hwaccelName = hw.Name
	}

	totalBytes, _ := s.db.TotalStorageBytes()

	writeJSON(w, http.StatusOK, map[string]any{
		"version":      "0.1.0",
		"uptime":       time.Since(startTime).String(),
		"hwaccel":      hwaccelName,
		"cameras":      len(statuses),
		"online":       onlineCount,
		"storage_bytes": totalBytes,
		"storage":      formatBytes(totalBytes),
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
      <span class="cam-live-dot {{if .HasMotion}}motion{{else if .Online}}{{else}}offline{{end}}"></span>
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
	totalBytes, _ := s.db.TotalStorageBytes()

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
		Storage:     formatBytes(totalBytes),
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
		fmt.Fprint(w, `<div class="empty-state"><p>No events recorded yet.</p></div>`)
		return
	}

	tmpl := template.Must(template.New("gallery").Funcs(s.funcMap).Parse(
		`{{range .}}` +
			`<a class="event-card" href="/event.html?id={{.ID}}" role="listitem">` +
			`<div class="event-thumb">` +
			`{{if .SnapshotPath}}<img src="/api/events/{{.ID}}/snapshot" alt="{{.Label}}" loading="lazy">` +
			`{{else}}<img src="/api/cameras/{{.CameraName}}/snapshot" alt="{{.Label}}" loading="lazy">{{end}}` +
			`<span class="event-label-badge {{.Label}}">{{.Label}}</span>` +
			`<span class="event-score-badge">{{scorePercent .Score}}</span>` +
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
		fmt.Fprintf(w, `<div id="load-more-trigger" hx-get="%s" hx-trigger="revealed" hx-swap="outerHTML"></div>`, nextURL)
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

	type eventDetailData struct {
		camera.Event
		PrevID string
		NextID string
	}

	data := eventDetailData{
		Event:  *event,
		PrevID: prevID,
		NextID: nextID,
	}

	tmpl := template.Must(template.New("detail").Funcs(s.funcMap).Parse(
		`<div class="page-header"><h1>{{.Label}} Detection</h1></div>` +
			`<div class="event-detail-layout">` +
			`<div class="event-media">` +
			`{{if .ClipPath}}<video controls autoplay><source src="/api/events/{{.ID}}/clip" type="video/mp4"></video>` +
			`{{else if .SnapshotPath}}<img src="/api/events/{{.ID}}/snapshot" alt="event snapshot">` +
			`{{else}}<img src="/api/cameras/{{.CameraName}}/snapshot" alt="event">{{end}}` +
			`</div>` +
			`<div class="event-sidebar">` +
			`<div class="meta-card">` +
			`<div class="meta-card-header">Details</div>` +
			`<div class="meta-row"><span class="key">Camera</span><span class="val">{{.CameraName}}</span></div>` +
			`<div class="meta-row"><span class="key">Label</span><span class="val">{{.Label}}</span></div>` +
			`<div class="meta-row"><span class="key">Confidence</span><span class="val">{{scorePercent .Score}}</span></div>` +
			`<div class="meta-row"><span class="key">Time</span><span class="val">{{formatTime .Timestamp}}</span></div>` +
			`<div class="meta-row"><span class="key">Event ID</span><span class="val mono">{{.ID}}</span></div>` +
			`</div>` +
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

	hwaccelName := "none"
	if hw := s.cameras.HWAccelBackend(); hw != nil {
		hwaccelName = hw.Name
	}

	uptime := time.Since(startTime)
	uptimeStr := formatDuration(uptime)

	totalBytes, _ := s.db.TotalStorageBytes()
	segCount, _ := s.db.CountSegments()
	byCamera, _ := s.db.SegmentBytesByCamera()

	type storageEntry struct {
		Camera  string
		Bytes   int64
		Display string
		Percent float64
	}

	storageEntries := make([]storageEntry, 0, len(byCamera))
	for cam, bytes := range byCamera {
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
		HWAccel     string
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
		HWAccel:     hwaccelName,
		GoVersion:   runtime.Version(),
		CameraCount: len(statuses),
		OnlineCount: onlineCount,
		Statuses:    statuses,
		TotalBytes:  totalBytes,
		TotalStr:    formatBytes(totalBytes),
		SegCount:    segCount,
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
    <div class="sys-row"><span class="key">HW Accel</span><span class="val">{{.HWAccel}}</span></div>
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

func (s *Server) handleRecordingsPartial(w http.ResponseWriter, r *http.Request) {
	cameraFilter := r.URL.Query().Get("camera")
	dateStr := r.URL.Query().Get("date")

	date := time.Now().UTC()
	if dateStr != "" {
		if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
			date = parsed
		}
	}

	if cameraFilter == "" {
		names := s.cameras.ListCameras()
		if len(names) > 0 {
			cameraFilter = names[0]
		}
	}

	if cameraFilter == "" {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div class="empty-state"><p>No cameras configured.</p></div>`)
		return
	}

	segments, err := s.db.GetSegmentsForDate(cameraFilter, date)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(segments) == 0 {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div class="empty-state"><p>No recordings for this date.</p></div>`)
		return
	}

	funcs := template.FuncMap{
		"formatTime":  s.funcMap["formatTime"],
		"formatBytes": formatBytes,
		"segDuration": func(start, end time.Time) string {
			return formatDuration(end.Sub(start))
		},
	}

	tmpl := template.Must(template.New("recordings").Funcs(funcs).Parse(
		`{{range .}}<div class="segment-row">` +
			`<span class="segment-time">{{formatTime .StartTime}} - {{formatTime .EndTime}}</span>` +
			`<span class="segment-duration">{{segDuration .StartTime .EndTime}}</span>` +
			`<span class="segment-size">{{formatBytes .SizeBytes}}</span>` +
			`</div>{{end}}`))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, segments); err != nil {
		slog.Error("template error", "error", err)
	}
}

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

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}

