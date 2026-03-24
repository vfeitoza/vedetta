package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// SetupHandler serves the onboarding API endpoints used during initial setup.
type SetupHandler struct {
	configPath string
	db         *storage.DB
	setupDone  chan struct{}
	mu         sync.Mutex
	thumbnails map[string][]byte // IP -> JPEG
	completed  bool
}

// NewSetupHandler creates a handler for the setup/onboarding API.
func NewSetupHandler(configPath string, db *storage.DB, setupDone chan struct{}) *SetupHandler {
	return &SetupHandler{
		configPath: configPath,
		db:         db,
		setupDone:  setupDone,
		thumbnails: make(map[string][]byte),
	}
}

// HandleSetup creates the initial admin account and writes config.
func (h *SetupHandler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password are required"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("bcrypt hash failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	passwordHash := string(hash)

	if err := h.db.SaveAuthUser(req.Username, passwordHash); err != nil {
		slog.Error("failed to save auth user", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save user"})
		return
	}

	if err := config.WriteInitialConfig(h.configPath, req.Username, passwordHash); err != nil {
		slog.Warn("config write failed, returning YAML for manual setup", "error", err)
		yamlContent, genErr := config.GenerateInitialConfigYAML(req.Username, passwordHash)
		if genErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate config"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "config_readonly",
			"config_yaml": yamlContent,
			"message":     fmt.Sprintf("Could not write config to %s. Save the YAML content manually.", h.configPath),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleDiscover runs WS-Discovery to find cameras on the network.
func (h *SetupHandler) HandleDiscover(w http.ResponseWriter, _ *http.Request) {
	cameras, err := camera.DiscoverCameras(5 * time.Second)
	if err != nil {
		slog.Error("camera discovery failed", "error", err)
		writeJSON(w, http.StatusOK, map[string]any{"cameras": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cameras": cameras})
}

// HandleProbe tests RTSP credentials against discovered cameras.
func (h *SetupHandler) HandleProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cameras []struct {
			IP           string `json:"ip"`
			Port         int    `json:"port"`
			Manufacturer string `json:"manufacturer"`
		} `json:"cameras"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	type probeResult struct {
		IP        string                 `json:"ip"`
		Status    string                 `json:"status"`
		Streams   []camera.StreamProfile `json:"streams,omitempty"`
		Thumbnail string                 `json:"thumbnail,omitempty"`
		Error     string                 `json:"error,omitempty"`
	}

	var results []probeResult
	for _, cam := range req.Cameras {
		port := cam.Port
		if port == 0 {
			port = 554
		}
		streams, err := camera.ProbeRTSPWithCredentials(cam.IP, port, cam.Manufacturer, req.Username, req.Password)
		if err != nil {
			status := "error"
			if err.Error() == "authentication failed" {
				status = "auth_failed"
			}
			results = append(results, probeResult{
				IP:     cam.IP,
				Status: status,
				Error:  err.Error(),
			})
			continue
		}

		// Grab thumbnail asynchronously using the sub stream (or first available)
		thumbnailURL := ""
		if len(streams) > 0 {
			thumbnailURL = fmt.Sprintf("/api/discover/thumbnail/%s", cam.IP)
			// Pick sub stream for thumbnail (smaller/faster), fall back to first
			streamURL := streams[0].URL
			for _, s := range streams {
				if s.Resolution == "sub" {
					streamURL = s.URL
					break
				}
			}
			go func(ip, rtspURL string) {
				data, err := camera.GrabThumbnail(rtspURL, 75)
				if err != nil {
					slog.Debug("thumbnail grab failed", "ip", ip, "error", err)
					return
				}
				h.mu.Lock()
				h.thumbnails[ip] = data
				h.mu.Unlock()
				slog.Info("thumbnail cached", "ip", ip, "size", len(data))
			}(cam.IP, streamURL)
		}

		results = append(results, probeResult{
			IP:        cam.IP,
			Status:    "ok",
			Streams:   streams,
			Thumbnail: thumbnailURL,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// HandleThumbnail serves a cached camera thumbnail JPEG.
func (h *SetupHandler) HandleThumbnail(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	h.mu.Lock()
	data, ok := h.thumbnails[ip]
	h.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

// HandleAddCameras writes camera entries to the config file.
func (h *SetupHandler) HandleAddCameras(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cameras []struct {
			Name      string `json:"name"`
			URL       string `json:"url"`
			RecordURL string `json:"record_url"`
		} `json:"cameras"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	var yamlSnippets []string
	for _, cam := range req.Cameras {
		enabled := true
		cc := config.CameraConfig{
			Name:      cam.Name,
			URL:       cam.URL,
			RecordURL: cam.RecordURL,
			Detect: config.DetectStreamConfig{
				Width:  640,
				Height: 480,
				FPS:    5,
			},
			Record: config.StreamConfig{
				Width:  1920,
				Height: 1080,
				FPS:    15,
			},
			Enabled: &enabled,
		}
		comment := fmt.Sprintf("Added during setup on %s", time.Now().Format("2006-01-02"))
		if err := config.AppendCamera(h.configPath, cc, comment); err != nil {
			slog.Warn("failed to append camera to config", "name", cam.Name, "error", err)
			snippet, genErr := config.GenerateCameraYAML(cc, comment)
			if genErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate camera YAML"})
				return
			}
			yamlSnippets = append(yamlSnippets, snippet)
		}
	}

	if len(yamlSnippets) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "config_readonly",
			"yaml_snippets": yamlSnippets,
			"message":       "Could not write to config file. Add the YAML snippets manually.",
		})
		return
	}

	h.signalComplete()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleComplete signals that setup is done (used by "Skip for Now").
func (h *SetupHandler) HandleComplete(w http.ResponseWriter, _ *http.Request) {
	h.signalComplete()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// signalComplete closes the setupDone channel exactly once.
func (h *SetupHandler) signalComplete() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.completed {
		h.completed = true
		close(h.setupDone)
	}
}
