package api

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/storage"
)

// --- HTML partial handlers for htmx ---

func (s *Server) handleCameraGridPartial(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()

	w.Header().Set("Content-Type", "text/html")

	if len(statuses) == 0 {
		const emptyHTML = `<div class="empty-hero">
  <div class="empty-icon">
    <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/>
      <circle cx="12" cy="13" r="4"/>
    </svg>
  </div>
  <div class="empty-title">No cameras configured yet</div>
  <div class="empty-desc">
    Vedetta can automatically discover cameras on your network using ONVIF. Or add one manually if you know the RTSP URL.
  </div>
  <div class="empty-actions">
    <a href="#" data-action-click="startDiscovery(); return false;" class="action-card primary">
      <div class="action-icon">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10"/>
          <path d="M2 12h20"/>
          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
        </svg>
      </div>
      <div class="action-text"><h3>Discover Cameras</h3><p>Scan your network for ONVIF cameras</p></div>
    </a>
    <a href="#" data-action-click="showAddManual(); return false;" class="action-card">
      <div class="action-icon">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
      </div>
      <div class="action-text"><h3>Add Manually</h3><p>Enter an RTSP URL directly</p></div>
    </a>
  </div>
</div>`
		fmt.Fprint(w, emptyHTML)
		return
	}

	type cameraCard struct {
		Name        string
		DisplayName string
		Online      bool
		HasMotion   bool
		Stopped     bool
	}

	cards := make([]cameraCard, 0, len(statuses))
	for _, st := range statuses {
		cards = append(cards, cameraCard{
			Name:        st.Name,
			DisplayName: displayName(st.Name),
			Online:      st.Online,
			HasMotion:   st.HasMotion,
			Stopped:     st.Stopped,
		})
	}

	tmpl := template.Must(template.New("grid").Parse(`{{range .}}<div class="cam-card{{if .Stopped}} cam-stopped{{end}}" data-action-click="location.href='/camera.html?name={{.Name}}'" role="listitem">
  <div class="cam-preview">
    <img src="/api/cameras/{{.Name}}/snapshot" alt="{{.Name}}" loading="lazy">
    <div class="cam-live-badge">
      {{if .Stopped}}<span class="cam-live-dot stopped"></span>STOPPED{{else}}<span class="cam-live-dot {{if .Online}}{{else}}offline{{end}}"></span>
      {{if .Online}}LIVE{{else}}OFFLINE{{end}}{{end}}
    </div>
    <button class="cam-toggle-btn" data-action-click="event.stopPropagation(); toggleCamera('{{.Name}}', {{.Stopped}})" title="{{if .Stopped}}Start camera{{else}}Stop camera{{end}}">
      {{if .Stopped}}<svg viewBox="0 0 24 24" fill="currentColor" width="16" height="16"><polygon points="5 3 19 12 5 21 5 3"/></svg>{{else}}<svg viewBox="0 0 24 24" fill="currentColor" width="16" height="16"><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></svg>{{end}}
    </button>
  </div>
  <div class="cam-footer">
    <span class="cam-name">{{.DisplayName}}</span>
  </div>
</div>{{end}}`))

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
	objectFilter := r.URL.Query().Get("object")
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

	events, err := s.db.QueryEventsFiltered(cameraFilter, labelFilter, "", objectFilter, limit, offset)
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
			`{{if .SubLabel}}<span class="event-object-badge">{{.SubLabel}}</span>{{end}}` +
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
	knownObjects, _ := s.db.ListKnownObjectsByLabel(event.Label)

	type namedPerson struct {
		ID   int64
		Name string
	}

	// Load named people for person events
	var namedPeople []namedPerson
	if event.Label == "person" {
		if people, err := s.db.ListPeople(); err == nil {
			for _, p := range people {
				if p.Name != "" && !p.Ignore {
					namedPeople = append(namedPeople, namedPerson{ID: p.ID, Name: p.Name})
				}
			}
		}
	}

	type eventDetailData struct {
		camera.Event
		PrevID       string
		NextID       string
		RecordingURL string
		HasRecording bool
		Duration     string
		Sightings    []storage.ObjectSighting
		KnownObjects []storage.KnownObject
		NamedPeople  []namedPerson
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
		KnownObjects: knownObjects,
		NamedPeople:  namedPeople,
	}

	tmpl := template.Must(template.New("detail").Funcs(s.funcMap).Parse(
		`<div class="page-header"><h1>{{.Label}} Detection</h1></div>` +
			`<div class="event-detail-layout">` +
			`<div class="event-media">` +
			`{{if .SnapshotAvailable}}<div class="detection-overlay-wrap" id="detection-wrap">` +
			`<img id="event-snapshot" src="/api/events/{{.ID}}/snapshot" alt="event snapshot" ` +
			`data-box-x1="{{index .Box 0}}" data-box-y1="{{index .Box 1}}" ` +
			`data-box-x2="{{index .Box 2}}" data-box-y2="{{index .Box 3}}" ` +
			`data-label="{{.Label}}" data-sub-label="{{.SubLabel}}" ` +
			`data-score="{{scorePercent .Score}}" data-event-id="{{.ID}}" ` +
			`data-render-detection-overlay="true">` +
			`</div>` +
			`{{else}}<img id="event-snapshot" src="/api/cameras/{{.CameraName}}/snapshot" alt="event">{{end}}` +
			// Prefer the pre-generated per-event clip (a plain MP4 file)
			// over the HLS re-segmenter path when both are available. The
			// clip plays natively in every browser including iOS Safari;
			// the HLS path splits multi-track moofs into single-track for
			// MSE/hls.js compatibility, which iOS's native HLS decoder
			// rejects with a silent black-frame failure. For longer
			// scrub-through-history viewing, the sidebar still offers
			// "View in Recording" which opens the camera playback page.
			`{{if .ClipAvailable}}<div class="play-overlay" id="play-overlay" data-action-click="playEventClip(this, '{{.ID}}')">` +
			`<svg viewBox="0 0 24 24" fill="white" width="64" height="64"><polygon points="5 3 19 12 5 21 5 3"/></svg>` +
			`</div>{{else if .HasRecording}}<div class="play-overlay" id="play-overlay" data-action-click="playEventRecording(this, '{{.CameraName}}', '{{.Timestamp.Format "2006-01-02T15:04:05Z07:00"}}')">` +
			`<svg viewBox="0 0 24 24" fill="white" width="64" height="64"><polygon points="5 3 19 12 5 21 5 3"/></svg>` +
			`</div>{{end}}` +
			`</div>` +
			`<div class="event-sidebar">` +
			`<div class="meta-card">` +
			`<div class="meta-card-header">Details</div>` +
			`<div class="meta-row"><span class="key">Camera</span><span class="val">{{.CameraName}}</span></div>` +
			`<div class="meta-row"><span class="key">Label</span><span class="val">{{.Label}}</span></div>` +
			`{{if .SubLabel}}<div class="meta-row"><span class="key">Identity</span><span class="val">{{.SubLabel}}</span></div>{{end}}` +
			`<div class="meta-row"><span class="key">Confidence</span><span class="val">{{scorePercent .Score}}</span></div>` +
			`<div class="meta-row"><span class="key">Time</span><span class="val">{{formatTime .Timestamp}}</span></div>` +
			`{{if .Duration}}<div class="meta-row"><span class="key">Duration</span><span class="val">{{.Duration}}</span></div>{{end}}` +
			`<div class="meta-row"><span class="key">Event ID</span><span class="val mono">{{.ID}}</span></div>` +
			`</div>` +
			`<div class="meta-card">` +
			`<div class="meta-card-header">Downloads</div>` +
			`{{if .ClipAvailable}}<a href="/api/events/{{.ID}}/clip?download=1" download class="download-row">` +
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
			`{{if .SubLabel}}<div class="meta-card-header">Tracked</div>` +
			`<div style="display:flex;gap:0.75rem;align-items:center">` +
			`<img src="/api/events/{{.ID}}/detection-crop" alt="detection" style="width:80px;height:auto;border-radius:var(--radius-sm);border:2px solid var(--green)">` +
			`<span style="font-size:var(--text-sm);color:var(--green);font-weight:600">{{.SubLabel}}</span>` +
			`</div>` +
			`{{else}}<div class="meta-card-header">Identify</div>` +
			`<div style="display:flex;gap:0.75rem;margin-bottom:0.5rem;align-items:center">` +
			`<img src="/api/events/{{.ID}}/detection-crop" alt="detection" style="width:64px;height:auto;border-radius:var(--radius-sm);border:2px solid var(--accent)">` +
			`<input type="text" id="identify-search" class="person-name-input" placeholder="Search or add new {{.Label}}..." ` +
			`style="flex:1;padding:0.5rem 0.7rem;font-size:var(--text-sm)" ` +
			`data-action-input="filterIdentifyResults(this.value)" ` +
			`data-identify-enter-id="{{.ID}}" data-identify-enter-label="{{.Label}}">` +
			`</div>` +
			`<div id="identify-grid" data-event-id="{{.ID}}" data-label="{{.Label}}"></div>` +
			`{{end}}` +
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

	openH264 := openH264StatusInfo()
	decoderName := "native Go"
	if openH264.Available {
		decoderName = "native Go + OpenH264"
	}

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
		Version              string
		Uptime               string
		Decoder              string
		GoVersion            string
		CameraCount          int
		OnlineCount          int
		Statuses             []camera.CameraStatus
		TotalBytes           int64
		TotalStr             string
		SegCount             int
		Storage              []storageEntry
		RecompressEnabled    bool
		RecompressRunning    bool
		RecompressLastRun    string
		RecompressSegments   int64
		RecompressBytesSaved string
		UpdateAvailable      bool
		UpdateVersion        string
		UpdateURL            string
		OpenH264             media.OpenH264Status
		OpenH264UI           openH264Presentation
		OpenH264SourceLabel  string
	}

	rc := stats.Recompression
	lastRunStr := "never"
	if !rc.LastRun.IsZero() {
		lastRunStr = rc.LastRun.Format("2006-01-02 15:04")
	}

	data := sysData{
		Version:              s.version,
		Uptime:               uptimeStr,
		Decoder:              decoderName,
		GoVersion:            runtime.Version(),
		CameraCount:          len(statuses),
		OnlineCount:          onlineCount,
		Statuses:             statuses,
		TotalBytes:           totalBytes,
		TotalStr:             formatBytes(totalBytes),
		SegCount:             stats.SegmentCount,
		Storage:              storageEntries,
		RecompressEnabled:    rc.Enabled,
		RecompressRunning:    rc.IsRunning,
		RecompressLastRun:    lastRunStr,
		RecompressSegments:   rc.SegmentsRecompressed,
		RecompressBytesSaved: formatBytes(rc.BytesReclaimed),
		OpenH264:             openH264,
		OpenH264UI:           describeOpenH264Status(openH264),
		OpenH264SourceLabel:  formatOpenH264Source(openH264.Source),
	}

	if s.updateChecker != nil {
		us := s.updateChecker.Status()
		if us.UpdateAvailable && !us.Dismissed {
			data.UpdateAvailable = true
			data.UpdateVersion = us.Latest
			data.UpdateURL = us.URL
		}
	}

	tmpl := template.Must(template.New("system").Funcs(s.funcMap).Parse(systemPartialTemplate))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("template error", "error", err)
	}
}

func formatOpenH264Source(source string) string {
	switch source {
	case "environment":
		return "Environment override"
	case "system":
		return "System library"
	case "installed":
		return "Vedetta installed"
	default:
		return ""
	}
}

const systemPartialTemplate = `<div class="sys-card">
  <div class="sys-card-header">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
    System Info
  </div>
  <div class="sys-card-body">
    <div class="sys-row"><span class="key">Version</span><span class="val">{{.Version}}{{if .UpdateAvailable}} <a href="/settings.html" style="color:var(--accent);font-size:0.85em;margin-left:0.5em">{{.UpdateVersion}} available</a>{{end}}</span></div>
    <div class="sys-row"><span class="key">Uptime</span><span class="val">{{.Uptime}}</span></div>
    <div class="sys-row"><span class="key">Decoder</span><span class="val">{{.Decoder}}</span></div>
    <div class="sys-row"><span class="key">Go</span><span class="val">{{.GoVersion}}</span></div>
    <div class="sys-row"><span class="key">Cameras</span><span class="val">{{.CameraCount}} ({{.OnlineCount}} online)</span></div>
  </div>
</div>
<div class="sys-card">
  <div class="sys-card-header">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 5h14v14H5z"/><path d="M9 9h6v6H9z"/><path d="M9 1v4M15 1v4M9 19v4M15 19v4M19 9h4M19 14h4M1 9h4M1 14h4"/></svg>
    Codec Status
  </div>
  <div class="sys-card-body">
    <div class="sys-row"><span class="key">OpenH264</span><span class="val">{{if eq .OpenH264UI.BadgeTone "success"}}<span class="green">{{.OpenH264UI.Badge}}</span>{{else if eq .OpenH264UI.BadgeTone "error"}}<span class="red">{{.OpenH264UI.Badge}}</span>{{else}}{{.OpenH264UI.Badge}}{{end}}</span></div>
    <div style="margin-top: 0.35rem; line-height: 1.45">
      <div>{{.OpenH264UI.Headline}}</div>
      {{if .OpenH264UI.Detail}}<div style="color: var(--muted); margin-top: 0.25rem">{{.OpenH264UI.Detail}}</div>{{end}}
    </div>
    {{if .OpenH264SourceLabel}}<div class="sys-row"><span class="key">Source</span><span class="val">{{.OpenH264SourceLabel}}</span></div>{{end}}
    {{if .OpenH264.Version}}<div class="sys-row"><span class="key">Version</span><span class="val">{{.OpenH264.Version}}</span></div>{{end}}
    {{if .OpenH264.Path}}<div class="sys-row"><span class="key">Path</span><span class="val" style="word-break: break-all">{{.OpenH264.Path}}</span></div>{{end}}
    {{if .OpenH264UI.ShowDiagnostics}}<details style="margin-top: 0.75rem"><summary style="cursor: pointer; color: var(--muted)">Technical details</summary><div style="margin-top: 0.5rem; color: var(--muted); line-height: 1.4; word-break: break-word">{{.OpenH264UI.Diagnostic}}</div></details>{{end}}
    {{if .OpenH264UI.ShowInstall}}
    <div style="margin-top: 0.75rem">
      {{if .OpenH264.Installing}}
      <button class="btn btn-sm" disabled>Installing…</button>
      {{else}}
      <button class="btn btn-sm" data-action-click="installOpenH264FromSystem(this)">{{.OpenH264UI.ActionLabel}}</button>
      {{end}}
    </div>
    {{end}}
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
</div>
{{if .RecompressEnabled}}<div class="sys-card">
  <div class="sys-card-header">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
    Recompression
  </div>
  <div class="sys-card-body">
    <div class="sys-row"><span class="key">Last run</span><span class="val">{{.RecompressLastRun}}</span></div>
    <div class="sys-row"><span class="key">Segments</span><span class="val">{{.RecompressSegments}}</span></div>
    <div class="sys-row"><span class="key">Space saved</span><span class="val">{{.RecompressBytesSaved}}</span></div>
    <div style="margin-top: 0.75rem">
      {{if .RecompressRunning}}
      <button class="btn btn-sm" disabled>Running…</button>
      {{else}}
      <button class="btn btn-sm"
        hx-post="/api/system/recompress/trigger"
        hx-swap="none"
        data-recompress-trigger="true">Run now</button>
      {{end}}
    </div>
  </div>
</div>{{end}}`
