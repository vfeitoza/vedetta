package camera

import (
	"image"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
)

// NewTestCamera builds a Camera with no RTSP/detector wiring, suitable for
// exercising HTTP handlers that depend on a registered camera. Production
// goroutines are not started; LiveFrame returns the frame set via
// SetTestFrame, or nil.
func NewTestCamera(name string) *Camera {
	return &Camera{
		config:          config.CameraConfig{Name: name},
		confirmedTracks: make(map[int]string),
		trackNames:      make(map[int]string),
		presenceTracker: NewPresenceTracker(),
		mu:              sync.RWMutex{},
	}
}

// SetTestFrame primes the camera with a synthetic frame. Subsequent calls
// to LiveFrame and LastSnapshot will return a copy of this frame.
func (c *Camera) SetTestFrame(img *image.RGBA) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if img == nil {
		c.rawFrame = nil
		c.frameW = 0
		c.frameH = 0
		return
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	rgb := make([]byte, w*h*3)
	for y := range h {
		for x := range w {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			off := (y*w + x) * 3
			rgb[off+0] = byte(r >> 8)
			rgb[off+1] = byte(g >> 8)
			rgb[off+2] = byte(b >> 8)
		}
	}
	c.rawFrame = rgb
	c.frameW = w
	c.frameH = h
}

// SetTestOnline overrides IsOnline to return the given value. Intended for
// handler tests that need deterministic online/offline state without a
// running RTSP source.
func (c *Camera) SetTestOnline(online bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := online
	c.testOnlineOverride = &v
}

// SetTestLastFrameTime primes lastFrameTime so handlers exercising
// Last-Modified or freshness logic have a deterministic timestamp.
func (c *Camera) SetTestLastFrameTime(ts time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastFrameTime = ts
}

// RegisterForTest installs a pre-built Camera into the manager without
// starting its RTSP/detect goroutines. Intended for handler tests.
func (m *Manager) RegisterForTest(cam *Camera) {
	if cam == nil || m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cameras == nil {
		m.cameras = map[string]*Camera{}
	}
	m.cameras[cam.config.Name] = cam
}
