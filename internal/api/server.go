package api

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/notify"
	"github.com/rvben/vedetta/internal/recording"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/storage"
	"github.com/rvben/vedetta/internal/stream"
	"github.com/rvben/vedetta/internal/update"
)

//go:embed static/*
var staticFiles embed.FS

var startTime = time.Now()

// MQTTPublisher is the subset of mqtt.Client used by the API server.
type MQTTPublisher interface {
	PublishSnapshot(cameraName, label string, jpegData []byte)
	PublishDoorbell(cameraName, person string, jpegData []byte)
}

type Server struct {
	version              string
	updateChecker        *update.Checker
	config               config.APIConfig
	auth                 *auth.Checker
	db                   *storage.DB
	cameras              *camera.Manager
	recorder             *recording.Recorder
	hub                  *rtsp.Hub
	streams              *stream.StreamManager
	mse                  *stream.MSEManager
	faceRecognizer       *detect.FaceRecognizer
	objectEmbedder       objectEmbedder
	ObjectMatchThreshold float64
	mqttClient           MQTTPublisher
	mqttEnabled          bool
	configPath           string
	mqttConfig           config.MQTTConfig
	detector             *detect.Detector
	recordingConfig      config.RecordingConfig
	restartRequired      bool
	hlsSegmentCache      sync.Map // map[string][]media.HLSSegmentRef — keyed by "camera:segID"
	snapshotPath         string
	faceCropDir          string
	ptzClients           map[string]*camera.PTZClient
	cameraConfigs        []config.CameraConfig
	httpSrv              *http.Server
	mux                  *http.ServeMux
	funcMap              template.FuncMap
	ready                atomic.Bool
	setupHandler         *SetupHandler
	setupMode            bool

	// SSE event bus for real-time browser notifications
	sseMu      sync.Mutex
	sseClients map[chan []byte]struct{}

	// Per-camera detection-frame fan-out for the live overlay.
	detectionHub *detectionHub

	objectRematchMu      sync.Mutex
	objectRematchRunning map[int64]bool
	objectRematchPending map[int64]bool
	faceBackfillRunning  atomic.Bool
	objectRematchFn      func(int64)

	// Push notification dispatcher and cached camera names. notifier is nil
	// when push is disabled (e.g. VAPID setup failed or operator opted out);
	// cameraNamesCached is populated by main.go after config load so the
	// prefs handler can render the full (camera × class) grid without
	// walking the live camera.Manager on every request.
	notifier          *notify.NotificationDispatcher
	cameraNamesCached []string

	// ctx is the application lifetime context (cancelled on shutdown).
	ctx context.Context
}

func New(cfg config.APIConfig, authChecker *auth.Checker, db *storage.DB) *Server {
	s := &Server{
		ctx:                  context.Background(),
		config:               cfg,
		auth:                 authChecker,
		db:                   db,
		mux:                  http.NewServeMux(),
		sseClients:           make(map[chan []byte]struct{}),
		detectionHub:         newDetectionHub(),
		objectRematchRunning: make(map[int64]bool),
		objectRematchPending: make(map[int64]bool),
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

	s.registerRoutes()

	return s
}

// NewSetupMode creates a Server that only serves setup/onboarding endpoints.
// No auth middleware is applied. The setupDone channel is closed when setup completes.
func NewSetupMode(cfg config.APIConfig, db *storage.DB, configPath string, setupDone chan struct{}) *Server {
	cfg = SetupModeAPIConfig(cfg)
	s := &Server{
		config:               cfg,
		db:                   db,
		mux:                  http.NewServeMux(),
		sseClients:           make(map[chan []byte]struct{}),
		setupMode:            true,
		objectRematchRunning: make(map[int64]bool),
		objectRematchPending: make(map[int64]bool),
	}

	sh := NewSetupHandler(configPath, db, setupDone)
	s.setupHandler = sh

	s.mux.HandleFunc("POST /api/setup", sh.HandleSetup)
	s.mux.HandleFunc("GET /api/setup/codecs/openh264", sh.HandleOpenH264Status)
	s.mux.HandleFunc("POST /api/setup/codecs/openh264/install", sh.HandleInstallOpenH264)
	s.mux.HandleFunc("GET /api/discover", sh.HandleDiscover)
	s.mux.HandleFunc("POST /api/discover/probe", sh.HandleProbe)
	s.mux.HandleFunc("GET /api/discover/thumbnail/{ip}", sh.HandleThumbnail)
	s.mux.HandleFunc("POST /api/setup/test-rtsp", sh.HandleTestRTSP)
	s.mux.HandleFunc("POST /api/cameras", sh.HandleAddCameras)
	s.mux.HandleFunc("POST /api/setup/complete", sh.HandleComplete)
	s.mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "setup", "admin_configured": sh.AdminConfigured()})
	})

	// Serve setup.html as default page
	staticSub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticSub))
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			r.URL.Path = "/setup.html"
		}
		fileServer.ServeHTTP(w, r)
	})

	// Catch-all: block non-setup API routes.
	// Uses GET and POST since those are the only methods not already covered by
	// setup-specific handlers above; this avoids mux conflict with "GET /".
	blockSetup := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "setup not complete"})
	}
	s.mux.HandleFunc("GET /api/", blockSetup)
	s.mux.HandleFunc("POST /api/", blockSetup)
	s.mux.HandleFunc("DELETE /api/", blockSetup)
	s.mux.HandleFunc("PUT /api/", blockSetup)
	s.mux.HandleFunc("PATCH /api/", blockSetup)

	return s
}

// Compile-time check: *Server must implement the generated ServerInterface.
var _ ServerInterface = (*Server)(nil)

// registerRoutes registers all application routes on s.mux.
// Called from New() and TransitionToFull().
func (s *Server) registerRoutes() {
	// Generated API route wiring — registers all OpenAPI-defined endpoints.
	// Path param extraction and query param parsing are handled by generated wrappers.
	HandlerFromMux(s, s.mux)

	// Zone snapshot reuses camera snapshot handler (not in OpenAPI spec)
	s.mux.HandleFunc("GET /api/cameras/{name}/zones/snapshot", func(w http.ResponseWriter, r *http.Request) {
		s.GetCameraSnapshot(w, r, r.PathValue("name"))
	})

	// HTML partial endpoints for htmx (not in OpenAPI spec)
	s.mux.HandleFunc("GET /partials/camera-grid", s.handleCameraGridPartial)
	s.mux.HandleFunc("GET /partials/dashboard-stats", s.handleDashboardStatsPartial)
	s.mux.HandleFunc("GET /partials/events-gallery", s.handleEventsGalleryPartial)
	s.mux.HandleFunc("GET /partials/event/{id}", s.handleEventDetailPartial)
	s.mux.HandleFunc("GET /partials/system-status", s.handleSystemStatusPartial)
	s.mux.HandleFunc("GET /partials/system", s.handleSystemPartial)

	// Camera management CRUD endpoints
	s.mux.HandleFunc("GET /api/cameras/manage", s.ListCamerasManage)
	s.mux.HandleFunc("POST /api/cameras/manage", s.AddCameraManage)
	s.mux.HandleFunc("PUT /api/cameras/manage/{index}", s.UpdateCameraManage)
	s.mux.HandleFunc("DELETE /api/cameras/manage/{index}", s.RemoveCameraManage)
	s.mux.HandleFunc("POST /api/cameras/test-rtsp", s.TestRTSPConnection)
	s.mux.HandleFunc("GET /api/cameras/{name}/detections", s.StreamCameraDetections)

	// Settings API endpoints
	s.mux.HandleFunc("GET /api/settings/mqtt", s.GetMQTTSettings)
	s.mux.HandleFunc("PUT /api/settings/mqtt", s.UpdateMQTTSettings)
	s.mux.HandleFunc("POST /api/settings/mqtt/test", s.TestMQTTConnection)
	s.mux.HandleFunc("GET /api/settings/mqtt/discover", s.DiscoverMQTTBrokers)
	s.mux.HandleFunc("GET /api/settings/recording", s.GetRecordingSettings)
	s.mux.HandleFunc("PUT /api/settings/recording", s.UpdateRecordingSettings)
	s.mux.HandleFunc("GET /api/settings/detect", s.GetDetectSettings)
	s.mux.HandleFunc("PUT /api/settings/detect", s.UpdateDetectSettings)
	s.mux.HandleFunc("GET /api/updates/status", s.GetUpdateStatus)
	s.mux.HandleFunc("GET /api/updates/check", s.CheckForUpdates)
	s.mux.HandleFunc("POST /api/updates/dismiss", s.DismissUpdate)
	s.mux.HandleFunc("GET /api/auth/info", s.GetAuthInfo)
	s.mux.HandleFunc("POST /api/auth/password", s.ChangePassword)
	s.mux.HandleFunc("GET /api/system/codecs/openh264", s.GetOpenH264Status)
	s.mux.HandleFunc("POST /api/system/codecs/openh264/install", s.InstallOpenH264)

	// Push notification endpoints
	s.mux.HandleFunc("GET /api/push/vapid-public-key", s.GetVAPIDPublicKey)
	s.mux.HandleFunc("POST /api/push/subscriptions", s.CreatePushSubscription)
	s.mux.HandleFunc("GET /api/push/subscriptions", s.ListPushSubscriptions)
	s.mux.HandleFunc("DELETE /api/push/subscriptions/{id}", s.DeletePushSubscription)
	s.mux.HandleFunc("GET /api/push/prefs", s.GetPushPrefs)
	s.mux.HandleFunc("PUT /api/push/prefs", s.PutPushPrefs)
	s.mux.HandleFunc("POST /api/push/test", s.TestPush)
	s.mux.HandleFunc("GET /api/push/snapshot/{id}", s.GetPushSnapshot)

	// Setup status endpoint (returns "running" in normal mode)
	s.mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
	})

	// Ensure .webmanifest files are served with the correct Content-Type.
	// mime.AddExtensionType is process-global and idempotent; http.FileServer
	// consults the mime package lazily on first request.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")

	// Serve static files at root, with extensionless-path redirects and an
	// app-shell 404 for unknown paths.
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		slog.Error("failed to create static sub filesystem", "error", err)
	} else {
		fileServer := http.FileServer(http.FS(staticSub))
		s.mux.Handle("GET /", staticFileHandler(staticSub, fileServer))
	}
}

// SetContext sets the application lifetime context used for background operations
// triggered by API requests (e.g. manual recompression).
func (s *Server) SetContext(ctx context.Context) {
	s.ctx = ctx
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)

	var handler http.Handler
	if s.setupMode {
		handler = securityHeadersMiddleware(apiBodyLimitMiddleware(s.mux))
	} else {
		handler = s.readyMiddleware(authMiddleware(s, apiBodyLimitMiddleware(s.mux)))
	}

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

func (s *Server) SetVersion(v string) {
	// Strip the -dirty suffix from version strings produced by `git describe
	// --dirty`. The full string (with -dirty) is useful for local diagnostics
	// but should not be shown to end users in the settings panel.
	s.version = strings.TrimSuffix(v, "-dirty")
}

func (s *Server) SetUpdateChecker(checker *update.Checker) {
	s.updateChecker = checker
}

func (s *Server) SetMQTT(publisher MQTTPublisher) {
	s.mqttClient = publisher
	s.mqttEnabled = true
}

func (s *Server) SetMQTTEnabled(enabled bool) {
	s.mqttEnabled = enabled
}

func (s *Server) SetConfigPath(path string) {
	s.configPath = path
}

func (s *Server) SetMQTTConfig(cfg config.MQTTConfig) {
	s.mqttConfig = cfg
}

func (s *Server) SetDetector(d *detect.Detector) {
	s.detector = d
}

func (s *Server) SetRecordingConfig(cfg config.RecordingConfig) {
	s.recordingConfig = cfg
}

// SetNotifier wires the push notification dispatcher. May be called with nil
// to keep push disabled — for example, when VAPID setup failed but the
// operator still wants the rest of the API to come up.
func (s *Server) SetNotifier(n *notify.NotificationDispatcher) {
	s.notifier = n
}

// SetCameraNames caches the list of configured camera names used by the
// push preferences handler to seed its (camera × class) response grid.
// Called from main.go after config load.
func (s *Server) SetCameraNames(names []string) {
	s.cameraNamesCached = append([]string(nil), names...)
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// SetupModeAPIConfig returns the API config for setup mode.
func SetupModeAPIConfig(cfg config.APIConfig) config.APIConfig {
	return cfg
}

func (s *Server) TransitionToFull(authChecker *auth.Checker) {
	s.auth = authChecker
	s.setupMode = false

	newMux := http.NewServeMux()
	s.mux = newMux
	s.registerRoutes()

	s.httpSrv.Handler = s.readyMiddleware(authMiddleware(s, apiBodyLimitMiddleware(newMux)))
}

func (s *Server) SetSubsystems(cameras *camera.Manager, recorder *recording.Recorder, hub *rtsp.Hub, faceRecognizer *detect.FaceRecognizer, objectEmbedder *detect.ObjectEmbedder, snapshotPath string, faceCropDir string, cameraConfigs []config.CameraConfig, ptzClients map[string]*camera.PTZClient) {
	s.cameras = cameras
	s.recorder = recorder
	s.hub = hub
	s.streams = stream.NewStreamManager(hub)
	s.mse = stream.NewMSEManager(hub, s.config.AllowedOrigins, s.config.TrustedProxies)
	s.faceRecognizer = faceRecognizer
	if objectEmbedder != nil {
		// Avoid the typed-nil-in-interface trap when callers pass a
		// nil *detect.ObjectEmbedder (the concrete type).
		s.objectEmbedder = objectEmbedder
	} else {
		s.objectEmbedder = nil
	}
	s.snapshotPath = snapshotPath
	s.faceCropDir = faceCropDir
	s.cameraConfigs = cameraConfigs
	s.ptzClients = ptzClients
	s.ready.Store(true)
	slog.Info("API server ready (all subsystems initialized)")
}

// readyMiddleware serves static files immediately but returns 503 for API/partial
// endpoints until subsystems are initialized.
func (s *Server) readyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ready.Load() && !isHealthProbePath(r) && (strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/partials/")) {
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

func (s *Server) cameraStatuses() []camera.CameraStatus {
	if s.cameras == nil {
		return nil
	}
	ordered := s.cameras.ListCameras()
	statuses := make([]camera.CameraStatus, 0, len(ordered))
	for _, name := range ordered {
		cam := s.cameras.GetCamera(name)
		if cam != nil {
			statuses = append(statuses, cam.Status())
		}
	}
	return statuses
}

// staticFileHandler wraps a standard file server with two behaviours:
//  1. Extensionless paths that have a corresponding .html file are
//     301-redirected to the .html URL (e.g. /settings → /settings.html).
//  2. Paths that don't match any file in the embedded FS are answered with a
//     styled app-shell "Page not found" response instead of the bare Go 404.
func staticFileHandler(fsys fs.FS, fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Redirect extensionless paths to their .html counterpart when it exists.
		if path != "/" && !strings.Contains(strings.TrimPrefix(path, "/"), ".") {
			htmlPath := strings.TrimPrefix(path, "/") + ".html"
			if _, err := fs.Stat(fsys, htmlPath); err == nil {
				http.Redirect(w, r, path+".html", http.StatusMovedPermanently)
				return
			}
		}

		// Intercept 404s from the file server. We buffer the response so that
		// we can substitute our app-shell page before anything is flushed to
		// the client.
		rr := &bufferedResponseWriter{code: http.StatusOK, header: make(http.Header)}
		fileServer.ServeHTTP(rr, r)
		if rr.code == http.StatusNotFound {
			// Discard the "404 page not found" plain-text body and replace it
			// with a styled app-shell page.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(appShell404HTML))
			return
		}
		// Flush the buffered response to the real writer.
		for k, vals := range rr.header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rr.code)
		_, _ = w.Write(rr.body)
	})
}

// bufferedResponseWriter captures the entire response (status + body) so the
// caller can inspect it before deciding whether to pass it through or replace it.
type bufferedResponseWriter struct {
	header http.Header
	body   []byte
	code   int
}

func (b *bufferedResponseWriter) Header() http.Header  { return b.header }
func (b *bufferedResponseWriter) WriteHeader(code int) { b.code = code }
func (b *bufferedResponseWriter) Write(p []byte) (int, error) {
	b.body = append(b.body, p...)
	return len(p), nil
}

// appShell404HTML is an app-shell page returned for unknown paths so that the
// user sees the Vedetta navigation rather than the bare Go default 404.
const appShell404HTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">
  <title>Page not found - Vedetta</title>
  <link rel="stylesheet" href="/style.css">
  <meta name="theme-color" content="#0a0e14">
  <link rel="icon" type="image/svg+xml" href="/favicon.svg">
</head>
<body>
  <nav class="topnav" role="navigation" aria-label="Main">
    <a href="/" class="topnav-brand" aria-label="Vedetta home">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>
      <span>vedetta</span>
    </a>
    <div class="topnav-links">
      <a href="/">Dashboard</a>
      <a href="/events.html">Events</a>
      <a href="/recordings.html">Recordings</a>
      <a href="/settings.html">Settings</a>
    </div>
  </nav>
  <main class="main-content" style="display:flex;align-items:center;justify-content:center;min-height:calc(100vh - var(--nav-h,52px))">
    <div class="card" style="text-align:center;padding:2.5rem 3rem;max-width:420px">
      <div style="font-size:3rem;margin-bottom:1rem">404</div>
      <h1 style="font-size:var(--text-lg,1.25rem);margin-bottom:.5rem">Page not found</h1>
      <p style="color:var(--text-secondary,#8b95a5);margin-bottom:1.5rem">The page you requested does not exist.</p>
      <a href="/" class="btn btn-primary">Go to Dashboard</a>
    </div>
  </main>
</body>
</html>`

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
