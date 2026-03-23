package camera

import (
	"context"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/rtsp"
)

// Manager manages all camera streams.
type Manager struct {
	cameras  map[string]*Camera
	order    []string // config-file order
	detector *detect.Detector
	events   chan<- Event
	hub      *rtsp.Hub
	mu       sync.RWMutex
}

func NewManager(configs []config.CameraConfig, detector *detect.Detector, motion config.MotionConfig, events chan<- Event, eventEnds chan<- EventEnd, presenceEvents chan<- PresenceEvent, hub *rtsp.Hub, snapshotPath string, snapshotQuality int, recordingPath string, faceRecognizer *detect.FaceRecognizer, faceEvents chan<- FaceEvent, faceCropDir string) *Manager {
	m := &Manager{
		cameras:  make(map[string]*Camera),
		detector: detector,
		events:   events,
		hub:      hub,
	}

	for _, cfg := range configs {
		if cfg.IsEnabled() {
			cam := NewCamera(cfg, detector, motion, events, eventEnds, presenceEvents, hub, snapshotPath, snapshotQuality, recordingPath, faceRecognizer, faceEvents, faceCropDir)
			m.cameras[cfg.Name] = cam
			m.order = append(m.order, cfg.Name)
		}
	}

	return m
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i, name := range m.order {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		if cam, ok := m.cameras[name]; ok {
			cam.Start(ctx)
		}
	}
}

func (m *Manager) GetCamera(name string) *Camera {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cameras[name]
}

func (m *Manager) ListCameras() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.order...)
}

// CameraStatuses returns the status of all managed cameras, sorted by name.
func (m *Manager) CameraStatuses() []CameraStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]CameraStatus, 0, len(m.order))
	for _, name := range m.order {
		if cam, ok := m.cameras[name]; ok {
			statuses = append(statuses, cam.Status())
		}
	}
	return statuses
}
