package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/stream"

	"github.com/pion/webrtc/v4"
)

// pickWebRTCRTSPURL chooses which RTSP source vedetta will negotiate over
// WebRTC. The default is the sub-stream because browser offers advertise
// H.264 level 3.1 at most, and a 1080p main stream at level 4.1 leaves
// Chrome's depacketizer stuck with framesAssembled=0. ?quality=high opts
// back into the record stream for clients that explicitly want full-res.
func pickWebRTCRTSPURL(detectURL, recordURL, quality string) string {
	if quality == "high" {
		return recordURL
	}
	return detectURL
}

func (s *Server) PostWebRTCOffer(w http.ResponseWriter, r *http.Request, name string) {
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

	rtspURL := pickWebRTCRTSPURL(cam.DetectURL(), cam.RecordURL(), r.URL.Query().Get("quality"))

	answer, err := s.streams.HandleOffer(name, rtspURL, offer)
	if err != nil {
		slog.Error("WebRTC offer failed", "camera", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "WebRTC negotiation failed"})
		return
	}

	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) GetMSEWebSocket(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	// Default to the high-res record stream. ?quality=low routes to the
	// detect substream for bandwidth-constrained clients (mobile, remote).
	rtspURL := cam.RecordURL()
	if r.URL.Query().Get("quality") == "low" {
		rtspURL = cam.DetectURL()
	}
	s.mse.HandleWebSocket(w, r, name, rtspURL)
}

// hlsRTSPURL mirrors GetMSEWebSocket: the high-res record stream by default
// (best quality, and the track that carries AAC audio), with ?quality=low
// routing to the detect substream for bandwidth-constrained clients.
func (s *Server) hlsRTSPURL(r *http.Request, cam *camera.Camera) string {
	if r.URL.Query().Get("quality") == "low" {
		return cam.DetectURL()
	}
	return cam.RecordURL()
}

func (s *Server) GetLiveHLS(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	rtspURL := s.hlsRTSPURL(r, cam)
	playlist, ok := s.hls.Playlist(rtspURL)
	if !ok {
		// The consumer is attached but no segment has been cut yet (waiting
		// for the first keyframe). Tell the client to retry shortly.
		slog.Info("HLS playlist not ready (warming up)",
			"camera", name, "quality", r.URL.Query().Get("quality"))
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "stream warming up"})
		return
	}
	slog.Debug("HLS playlist served", "camera", name,
		"quality", r.URL.Query().Get("quality"), "bytes", len(playlist))

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(playlist))
}

func (s *Server) GetLiveHLSInit(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	init, ok := s.hls.InitSegment(s.hlsRTSPURL(r, cam))
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "stream warming up"})
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(init)
}

func (s *Server) GetLiveHLSSegment(w http.ResponseWriter, r *http.Request, name string, segNum int64) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	seg, ok := s.hls.Segment(s.hlsRTSPURL(r, cam), uint64(segNum))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "segment not found"})
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(seg)
}

func (s *Server) GetMJPEG(w http.ResponseWriter, r *http.Request, name string) {
	cam := s.cameras.GetCamera(name)
	if cam == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
		return
	}

	handler := stream.MJPEGHandlerRGB24(cam.SnapshotRGB24, cam.FrameSize())
	handler.ServeHTTP(w, r)
}
