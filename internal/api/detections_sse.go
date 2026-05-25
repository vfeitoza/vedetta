package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

type detectionSubscriber struct {
	cameraName string
	ch         chan camera.DetectionFrame
}

// detectionHub fans DetectionFrame values out to per-camera SSE subscribers.
// Slow subscribers get their frames dropped rather than blocking publishers.
type detectionHub struct {
	mu      sync.RWMutex
	subs    map[string]map[*detectionSubscriber]struct{}
	dropped atomic.Int64 // detection frames dropped to full subscriber buffers
}

func newDetectionHub() *detectionHub {
	return &detectionHub{subs: make(map[string]map[*detectionSubscriber]struct{})}
}

func (h *detectionHub) Subscribe(cameraName string) *detectionSubscriber {
	sub := &detectionSubscriber{
		cameraName: cameraName,
		ch:         make(chan camera.DetectionFrame, 4),
	}
	h.mu.Lock()
	set, ok := h.subs[cameraName]
	if !ok {
		set = make(map[*detectionSubscriber]struct{})
		h.subs[cameraName] = set
	}
	set[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *detectionHub) Unsubscribe(sub *detectionSubscriber) {
	h.mu.Lock()
	if set, ok := h.subs[sub.cameraName]; ok {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.subs, sub.cameraName)
		}
	}
	h.mu.Unlock()
	close(sub.ch)
}

func (h *detectionHub) Publish(frame camera.DetectionFrame) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subs[frame.Camera] {
		select {
		case sub.ch <- frame:
		default:
			h.dropped.Add(1)
		}
	}
}

// DroppedFrames returns the cumulative count of detection frames dropped
// because a subscriber's buffer was full. A rising count means the live
// overlay is silently degrading for slow clients.
func (h *detectionHub) DroppedFrames() int64 { return h.dropped.Load() }

// PublishDetection is called from the main event loop to push a frame onto
// the hub for fan-out to subscribers.
func (s *Server) PublishDetection(frame camera.DetectionFrame) {
	if s.detectionHub == nil {
		return
	}
	s.detectionHub.Publish(frame)
}

// StreamCameraDetections serves a per-camera Server-Sent Events stream of
// DetectionFrame values for the live bounding-box overlay.
func (s *Server) StreamCameraDetections(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.cameras == nil || s.cameras.GetCamera(name) == nil {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	sub := s.detectionHub.Subscribe(name)
	defer s.detectionHub.Unsubscribe(sub)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case frame, ok := <-sub.ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprint(w, "data: "); err != nil {
				return
			}
			if err := enc.Encode(frame); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
