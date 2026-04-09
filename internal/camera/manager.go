package camera

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/rtsp"
)

// Manager manages all camera streams.
type Manager struct {
	cameras         map[string]*Camera
	cancelFuncs     map[string]context.CancelFunc
	order           []string // config-file order
	detector        *detect.Detector
	motionCfg       config.MotionConfig
	events          chan<- Event
	eventEnds       chan<- EventEnd
	presenceEvents  chan<- PresenceEvent
	hub             *rtsp.Hub
	snapshotPath    string
	snapshotQuality int
	recordingPath   string
	faceRecognizer  *detect.FaceRecognizer
	faceEvents      chan<- FaceEvent
	faceCropDir     string
	motionActivity  chan<- MotionActivity
	mu              sync.RWMutex
}

func NewManager(configs []config.CameraConfig, detector *detect.Detector, motion config.MotionConfig, events chan<- Event, eventEnds chan<- EventEnd, presenceEvents chan<- PresenceEvent, hub *rtsp.Hub, snapshotPath string, snapshotQuality int, recordingPath string, faceRecognizer *detect.FaceRecognizer, faceEvents chan<- FaceEvent, faceCropDir string, motionActivity chan<- MotionActivity) *Manager {
	m := &Manager{
		cameras:         make(map[string]*Camera),
		cancelFuncs:     make(map[string]context.CancelFunc),
		detector:        detector,
		motionCfg:       motion,
		events:          events,
		eventEnds:       eventEnds,
		presenceEvents:  presenceEvents,
		hub:             hub,
		snapshotPath:    snapshotPath,
		snapshotQuality: snapshotQuality,
		recordingPath:   recordingPath,
		faceRecognizer:  faceRecognizer,
		faceEvents:      faceEvents,
		faceCropDir:     faceCropDir,
		motionActivity:  motionActivity,
	}

	for _, cfg := range configs {
		if cfg.IsEnabled() {
			cam := NewCamera(cfg, detector, motion, events, eventEnds, presenceEvents, hub, snapshotPath, snapshotQuality, recordingPath, faceRecognizer, faceEvents, faceCropDir, motionActivity)
			m.cameras[cfg.Name] = cam
			m.order = append(m.order, cfg.Name)
		}
	}

	return m
}

func (m *Manager) Start(ctx context.Context, stoppedCameras map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, name := range m.order {
		if stoppedCameras[name] {
			slog.Info("skipping stopped camera", "name", name)
			continue
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		if cam, ok := m.cameras[name]; ok {
			camCtx, camCancel := context.WithCancel(ctx)
			m.cancelFuncs[name] = camCancel
			cam.Start(camCtx)
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

// CameraStatuses returns the status of all managed cameras in config-file order.
func (m *Manager) CameraStatuses() []CameraStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]CameraStatus, 0, len(m.order))
	for _, name := range m.order {
		if cam, ok := m.cameras[name]; ok {
			st := cam.Status()
			_, running := m.cancelFuncs[name]
			st.Stopped = !running
			statuses = append(statuses, st)
		}
	}
	return statuses
}

// AddCamera adds a new camera to the manager at runtime. If a camera with the
// same name already exists, the call is a no-op.
func (m *Manager) AddCamera(cfg config.CameraConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.cameras[cfg.Name]; exists {
		return
	}
	cam := NewCamera(cfg, m.detector, m.motionCfg, m.events, m.eventEnds, m.presenceEvents,
		m.hub, m.snapshotPath, m.snapshotQuality, m.recordingPath,
		m.faceRecognizer, m.faceEvents, m.faceCropDir, m.motionActivity)
	m.cameras[cfg.Name] = cam
	m.order = append(m.order, cfg.Name)
}

// StartCamera starts the named camera with its own derived context.
func (m *Manager) StartCamera(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cam, ok := m.cameras[name]
	if !ok {
		return fmt.Errorf("camera %q not found", name)
	}
	if _, running := m.cancelFuncs[name]; running {
		return fmt.Errorf("camera %q is already running", name)
	}

	camCtx, camCancel := context.WithCancel(ctx)
	m.cancelFuncs[name] = camCancel
	cam.Start(camCtx)
	return nil
}

// StopCamera cancels the context for the named camera, stopping its goroutine.
func (m *Manager) StopCamera(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.cameras[name]; !ok {
		return fmt.Errorf("camera %q not found", name)
	}
	cancel, ok := m.cancelFuncs[name]
	if !ok {
		return fmt.Errorf("camera %q is already stopped", name)
	}
	cancel()
	delete(m.cancelFuncs, name)
	return nil
}

// IsStopped returns true when the named camera exists but has no active context.
// Returns false for unknown camera names.
func (m *Manager) IsStopped(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.cameras[name]; !exists {
		return false
	}
	_, running := m.cancelFuncs[name]
	return !running
}
