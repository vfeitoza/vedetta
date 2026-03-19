package camera

import (
	"context"
	"sync"

	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/detect"
)

// Manager manages all camera streams.
type Manager struct {
	cameras  map[string]*Camera
	detector *detect.Detector
	events   chan<- Event
	mu       sync.RWMutex
}

func NewManager(configs []config.CameraConfig, detector *detect.Detector, events chan<- Event) *Manager {
	m := &Manager{
		cameras:  make(map[string]*Camera),
		detector: detector,
		events:   events,
	}

	for _, cfg := range configs {
		if cfg.Enabled {
			cam := NewCamera(cfg, detector, events)
			m.cameras[cfg.Name] = cam
		}
	}

	return m
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, cam := range m.cameras {
		cam.Start(ctx)
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
