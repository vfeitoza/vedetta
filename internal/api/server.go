package api

import (
	"encoding/json"
	"fmt"
	"image/jpeg"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/rvben/watchpost/internal/camera"
	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/storage"
)

type Server struct {
	config  config.APIConfig
	db      *storage.DB
	cameras *camera.Manager
	mux     *http.ServeMux
}

func New(cfg config.APIConfig, db *storage.DB, cameras *camera.Manager) *Server {
	s := &Server{
		config:  cfg,
		db:      db,
		cameras: cameras,
		mux:     http.NewServeMux(),
	}

	s.mux.HandleFunc("GET /api/cameras", s.handleListCameras)
	s.mux.HandleFunc("GET /api/cameras/{name}/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("GET /api/events", s.handleListEvents)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	return s
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	slog.Info("API server listening", "addr", addr)
	return http.ListenAndServe(addr, s.mux)
}

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

	events, err := s.db.QueryEvents(cameraFilter, labelFilter, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
