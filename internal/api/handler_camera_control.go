package api

import (
	"log/slog"
	"net/http"
)

// StopCamera handles POST /api/cameras/{name}/stop.
func (s *Server) StopCamera(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "camera not found: " + name,
		})
		return
	}

	if err := s.cameras.StopCamera(name); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": err.Error(),
		})
		return
	}

	s.recorder.StopCameraRecording(name)

	if err := s.db.SetCameraStopped(name, true); err != nil {
		slog.Error("failed to persist camera stopped state", "camera", name, "error", err)
	}

	slog.Info("camera stopped", "name", name)

	st := cam.Status()
	st.Stopped = true
	writeJSON(w, http.StatusOK, st)
}

// StartCamera handles POST /api/cameras/{name}/start.
func (s *Server) StartCamera(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "camera not found: " + name,
		})
		return
	}

	if err := s.db.SetCameraStopped(name, false); err != nil {
		slog.Error("failed to clear camera stopped state", "camera", name, "error", err)
	}

	if err := s.cameras.StartCamera(r.Context(), name); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": err.Error(),
		})
		return
	}

	s.recorder.StartCameraRecording(r.Context(), name)

	slog.Info("camera started", "name", name)

	st := cam.Status()
	st.Stopped = false
	writeJSON(w, http.StatusOK, st)
}
