package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rvben/vedetta/internal/metrics"
	"github.com/rvben/vedetta/internal/recording"
)

func (s *Server) GetOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	spec, err := GetSwagger()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load spec"})
		return
	}
	// Clear servers so clients use relative URLs
	spec.Servers = nil
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}

func (s *Server) GetHealth(w http.ResponseWriter, _ *http.Request) {
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
	// Storage projection can also trip degraded state before disk_low fires —
	// for example when the config's steady-state exceeds disk capacity.
	if storageStats.Projection.Status == "insufficient" ||
		storageStats.Projection.Status == "critical" ||
		storageStats.Projection.Status == "warning" {
		status = "degraded"
	}

	mqttStatus := "disabled"
	if s.mqttClient != nil {
		mqttStatus = "connected"
	} else if s.mqttEnabled {
		mqttStatus = "disconnected"
		status = "degraded"
	}

	// Detection check — reflects whether the H264 decoder is loaded and
	// the detection pipeline can produce events. When the codec is
	// unavailable, detection silently fails — this exposes that state to
	// monitoring and integrations so alerts can fire.
	detectionState := "ok"
	detectionReason := ""
	openH264 := openH264StatusInfo()
	if !openH264.Available {
		detectionState = "disabled"
		detectionReason = "OpenH264 codec not loaded"
		if openH264.Error != "" {
			detectionReason += ": " + openH264.Error
		}
		status = "degraded"
	}
	detectionCheck := map[string]any{
		"state":           detectionState,
		"openh264_loaded": openH264.Available,
	}
	if detectionReason != "" {
		detectionCheck["reason"] = detectionReason
	}
	if openH264.Version != "" {
		detectionCheck["openh264_version"] = openH264.Version
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": status,
		"checks": map[string]any{
			"database":  dbStatus,
			"mqtt":      mqttStatus,
			"detection": detectionCheck,
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
				"recompression": map[string]any{
					"enabled":               storageStats.Recompression.Enabled,
					"is_running":            storageStats.Recompression.IsRunning,
					"segments_recompressed": storageStats.Recompression.SegmentsRecompressed,
					"clips_recompressed":    storageStats.Recompression.ClipsRecompressed,
					"bytes_reclaimed":       storageStats.Recompression.BytesReclaimed,
					"last_run":              storageStats.Recompression.LastRun,
				},
				"projection": storageStats.Projection,
			},
		},
		"version": s.version,
		"uptime":  formatDuration(time.Since(startTime)),
	})
}

func (s *Server) GetHealthLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime": formatDuration(time.Since(startTime)),
	})
}

func (s *Server) GetHealthReady(w http.ResponseWriter, _ *http.Request) {
	statusCode := http.StatusOK
	status := "ready"

	checks := map[string]any{
		"initialized": s.ready.Load(),
	}

	if !s.ready.Load() {
		status = "starting"
		statusCode = http.StatusServiceUnavailable
	}

	if err := s.db.Ping(); err != nil {
		status = "degraded"
		statusCode = http.StatusServiceUnavailable
		checks["database"] = err.Error()
	} else {
		checks["database"] = "ok"
	}

	cameraStatuses := s.cameraStatuses()
	degraded := 0
	for _, st := range cameraStatuses {
		if st.Degraded {
			degraded++
		}
	}
	checks["cameras"] = map[string]any{
		"total":    len(cameraStatuses),
		"degraded": degraded,
	}
	if degraded > 0 {
		status = "degraded"
		statusCode = http.StatusServiceUnavailable
	}

	if s.recorder != nil {
		storageStats := s.recorder.StorageStats()
		checks["storage"] = map[string]any{
			"disk_low":         storageStats.DiskLow,
			"recording_paused": storageStats.RecordingPaused,
		}
		if storageStats.DiskLow {
			status = "degraded"
			statusCode = http.StatusServiceUnavailable
		}
	}

	writeJSON(w, statusCode, map[string]any{
		"status": status,
		"checks": checks,
	})
}

func (s *Server) GetMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	cameraStatuses := s.cameraStatuses()
	online := 0
	degraded := 0
	for _, st := range cameraStatuses {
		if st.Online {
			online++
		}
		if st.Degraded {
			degraded++
		}
	}

	var storageStats recording.StorageStats
	if s.recorder != nil {
		storageStats = s.recorder.StorageStats()
	}
	eventCount, _ := s.db.CountEvents()
	segmentCount, _ := s.db.CountSegments()

	var b strings.Builder
	fmt.Fprintf(&b, "vedetta_up 1\n")
	fmt.Fprintf(&b, "vedetta_ready %d\n", boolMetric(s.ready.Load()))
	fmt.Fprintf(&b, "vedetta_cameras_total %d\n", len(cameraStatuses))
	fmt.Fprintf(&b, "vedetta_cameras_online %d\n", online)
	fmt.Fprintf(&b, "vedetta_cameras_degraded %d\n", degraded)
	fmt.Fprintf(&b, "vedetta_events_total %d\n", eventCount)
	fmt.Fprintf(&b, "vedetta_segments_total %d\n", segmentCount)
	fmt.Fprintf(&b, "vedetta_storage_bytes %d\n", storageStats.TotalBytes)
	fmt.Fprintf(&b, "vedetta_disk_available_bytes %d\n", storageStats.DiskAvailable)
	fmt.Fprintf(&b, "vedetta_recording_paused %d\n", boolMetric(storageStats.RecordingPaused))
	fmt.Fprintf(&b, "vedetta_disk_low %d\n", boolMetric(storageStats.DiskLow))
	for _, st := range cameraStatuses {
		fmt.Fprintf(&b, "vedetta_camera_online{camera=%q} %d\n", promLabel(st.Name), boolMetric(st.Online))
		fmt.Fprintf(&b, "vedetta_camera_degraded{camera=%q} %d\n", promLabel(st.Name), boolMetric(st.Degraded))
		// A flapping camera (repeatedly dropping its RTSP connection) shows up
		// as a rising reconnect rate here, distinct from a steadily-offline one.
		if s.cameras != nil {
			if cam := s.cameras.GetCamera(st.Name); cam != nil {
				fmt.Fprintf(&b, "vedetta_camera_reconnects_total{camera=%q} %d\n", promLabel(st.Name), cam.Reconnects())
			}
		}
	}

	// Drop-on-full fan-out counters: the detection-overlay SSE hub and the MSE
	// pipeline shed frames to slow clients rather than blocking. A rising count
	// means live overlay / playback is silently degrading for those viewers.
	if s.detectionHub != nil {
		fmt.Fprintf(&b, "vedetta_detection_frames_dropped_total %d\n", s.detectionHub.DroppedFrames())
	}
	if s.mse != nil {
		fmt.Fprintf(&b, "vedetta_mse_frames_dropped_total %d\n", s.mse.DroppedFrames())
	}

	// Push notification counters — only emitted when a dispatcher is wired.
	if s.notifier != nil {
		s.notifier.Metrics().WriteProm(&b)
	}

	// Detection-pipeline latency histograms and frame counters (motion-detect,
	// YOLO inference, decode, frames processed/decoded/dropped).
	metrics.WriteProm(&b)

	_, _ = w.Write([]byte(b.String()))
}

func boolMetric(v bool) int {
	if v {
		return 1
	}
	return 0
}

func promLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}

func (s *Server) GetSystem(w http.ResponseWriter, _ *http.Request) {
	statuses := s.cameraStatuses()
	onlineCount := 0
	for _, st := range statuses {
		if st.Online {
			onlineCount++
		}
	}

	stats := s.recorder.StorageStats()
	openH264 := openH264StatusInfo()
	openH264Resp := openH264StatusResponseFor(openH264)
	decoder := "native Go"
	if openH264.Available {
		decoder = "native Go + OpenH264"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":       s.version,
		"uptime":        time.Since(startTime).String(),
		"decoder":       decoder,
		"cameras":       len(statuses),
		"online":        onlineCount,
		"storage_bytes": stats.TotalBytes,
		"storage":       formatBytes(stats.TotalBytes),
		"codecs": map[string]any{
			"openh264": openH264Resp,
		},
	})
}

func (s *Server) TriggerRecompression(w http.ResponseWriter, r *http.Request) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.recorder.TriggerRecompression(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
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
