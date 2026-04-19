package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rvben/vedetta/internal/rtsp"
)

// TestRTSPConnection dials an RTSP URL (DESCRIBE only) and reports codec,
// resolution, and audio info. Used by the UI "Test connection" button.
func (s *Server) TestRTSPConnection(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	var req struct {
		URL            string `json:"url"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}

	timeout := 5 * time.Second
	if req.TimeoutSeconds > 0 && req.TimeoutSeconds <= 30 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := rtsp.Probe(ctx, req.URL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"codec":       result.VideoCodec,
		"width":       result.Width,
		"height":      result.Height,
		"has_audio":   result.HasAudio,
		"audio_codec": result.AudioCodec,
	})
}
