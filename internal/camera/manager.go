package camera

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/rtsp"
)

// Manager manages all camera streams.
type Manager struct {
	cameras  map[string]*Camera
	detector *detect.Detector
	events   chan<- Event
	hub      *rtsp.Hub
	mu       sync.RWMutex
}

func NewManager(configs []config.CameraConfig, detector *detect.Detector, events chan<- Event, hub *rtsp.Hub, snapshotPath string, snapshotQuality int) *Manager {
	m := &Manager{
		cameras:  make(map[string]*Camera),
		detector: detector,
		events:   events,
		hub:      hub,
	}

	for _, cfg := range configs {
		if cfg.Enabled {
			cam := NewCamera(cfg, detector, events, hub, snapshotPath, snapshotQuality)
			m.cameras[cfg.Name] = cam
		}
	}

	return m
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	first := true
	for _, cam := range m.cameras {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		cam.Start(ctx)
		first = false
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

	names := make([]string, 0, len(m.cameras))
	for name := range m.cameras {
		names = append(names, name)
	}
	return names
}

// CameraStatuses returns the status of all managed cameras, sorted by name.
func (m *Manager) CameraStatuses() []CameraStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]CameraStatus, 0, len(m.cameras))
	for _, cam := range m.cameras {
		statuses = append(statuses, cam.Status())
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses
}
