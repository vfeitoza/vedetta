package camera

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/rtsp"
)

// Event represents a detected object event from a camera.
type Event struct {
	ID           string    `json:"id"`
	CameraName   string    `json:"camera"`
	Label        string    `json:"label"`
	Score        float32   `json:"score"`
	Box          [4]int    `json:"box"` // x1, y1, x2, y2
	Timestamp    time.Time `json:"timestamp"`
	SnapshotPath string    `json:"snapshot_path,omitempty"`
	ClipPath     string    `json:"clip_path,omitempty"`
}

// Camera manages a single RTSP camera stream.
type Camera struct {
	config         config.CameraConfig
	detector       *detect.Detector
	tracker        *detect.Tracker
	motionDetector *detect.MotionDetector
	events         chan<- Event
	hub            *rtsp.Hub

	mu               sync.RWMutex
	rawFrame         []byte // RGB24 frame data, guarded by mu
	frameW, frameH   int
	lastMotion       time.Time
	lastFrameTime    time.Time
	lastSnapshotSave time.Time
	confirmedTracks  map[int]bool
}

// CameraStatus represents the current status of a camera.
type CameraStatus struct {
	Name      string    `json:"name"`
	Online    bool      `json:"online"`
	HasMotion bool      `json:"has_motion"`
	LastFrame time.Time `json:"last_frame"`
}

func NewCamera(cfg config.CameraConfig, detector *detect.Detector, events chan<- Event, hub *rtsp.Hub) *Camera {
	return &Camera{
		config:          cfg,
		detector:        detector,
		tracker:         detect.NewTracker(30, 3),
		motionDetector:  detect.NewMotionDetector(25, 200, 0.05),
		events:          events,
		hub:             hub,
		confirmedTracks: make(map[int]bool),
	}
}

func (c *Camera) Name() string {
	return c.config.Name
}

func (c *Camera) RecordURL() string {
	if c.config.RecordURL != "" {
		return c.config.RecordURL
	}
	return c.config.URL
}

// LastSnapshot converts the stored RGB24 frame to RGBA on demand.
func (c *Camera) LastSnapshot() *image.RGBA {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rawFrame == nil {
		return nil
	}
	return rawToRGBA(c.rawFrame, c.frameW, c.frameH)
}

// snapshotPath returns the path for the cached latest snapshot.
func (c *Camera) snapshotPath() string {
	return filepath.Join("recordings", c.config.Name, "latest.jpg")
}

// loadCachedSnapshot loads the last saved snapshot from disk so offline cameras
// still have an image to show.
func (c *Camera) loadCachedSnapshot() {
	path := c.snapshotPath()
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	img, err := jpeg.Decode(f)
	if err != nil {
		slog.Warn("failed to decode cached snapshot", "camera", c.config.Name, "error", err)
		return
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	rgb := make([]byte, w*h*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			off := ((y-bounds.Min.Y)*w + (x - bounds.Min.X)) * 3
			rgb[off] = byte(r >> 8)
			rgb[off+1] = byte(g >> 8)
			rgb[off+2] = byte(b >> 8)
		}
	}

	c.mu.Lock()
	c.rawFrame = rgb
	c.frameW = w
	c.frameH = h
	c.mu.Unlock()
	slog.Info("loaded cached snapshot", "camera", c.config.Name)
}

// saveCachedSnapshot writes the current frame to disk (throttled to every 30s).
func (c *Camera) saveCachedSnapshot() {
	c.mu.RLock()
	if c.rawFrame == nil {
		c.mu.RUnlock()
		return
	}
	if time.Since(c.lastSnapshotSave) < 30*time.Second {
		c.mu.RUnlock()
		return
	}
	img := rawToRGBA(c.rawFrame, c.frameW, c.frameH)
	c.mu.RUnlock()

	path := c.snapshotPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	f.Close()
	os.Rename(tmp, path)

	c.mu.Lock()
	c.lastSnapshotSave = time.Now()
	c.mu.Unlock()
}

// Start begins reading frames from the RTSP stream via the Hub.
func (c *Camera) Start(ctx context.Context) {
	slog.Info("starting camera", "name", c.config.Name, "url", rtsp.SanitizeURL(c.config.URL))
	c.loadCachedSnapshot()

	go c.readFrames(ctx)
}

// readFrames connects to the RTSP stream via the Hub and processes detection frames.
func (c *Camera) readFrames(ctx context.Context) {
	w := c.config.Detect.Width
	h := c.config.Detect.Height
	fps := c.config.Detect.FPS

	source := c.hub.GetOrCreate(c.config.URL)

	// Wait for the source to connect and provide track info
	var videoTrack *rtsp.TrackInfo
	for {
		videoTrack = source.VideoTrack()
		if videoTrack != nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	consumer := media.NewDetectConsumer(w, h, fps, videoTrack)
	source.AddConsumer(consumer)
	defer source.RemoveConsumer(consumer)

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-consumer.Frames():
			c.processFrame(frame.Data, frame.Width, frame.Height)
		}
	}
}

// processFrame handles a decoded RGB24 frame — motion detection + YOLO.
func (c *Camera) processFrame(buf []byte, w, h int) {
	frameSize := w * h * 3

	// Store raw RGB24 frame for on-demand snapshot conversion.
	c.mu.Lock()
	if c.rawFrame == nil || len(c.rawFrame) != frameSize {
		c.rawFrame = make([]byte, frameSize)
	}
	copy(c.rawFrame, buf)
	c.frameW = w
	c.frameH = h
	c.lastFrameTime = time.Now()
	c.mu.Unlock()

	// Periodically save snapshot to disk for offline display
	c.saveCachedSnapshot()

	// Contour-based motion detection
	motionRegions := c.motionDetector.Detect(buf, w, h)
	if len(motionRegions) > 0 {
		c.mu.Lock()
		c.lastMotion = time.Now()
		c.mu.Unlock()

		detections := c.detector.DetectRGB24(buf, w, h)
		tracked := c.tracker.Update(detections)

		// Emit events for newly confirmed tracks
		for _, obj := range tracked {
			if !c.confirmedTracks[obj.TrackID] {
				c.confirmedTracks[obj.TrackID] = true
				c.events <- Event{
					ID:         fmt.Sprintf("%s-t%d-%d", c.config.Name, obj.TrackID, time.Now().UnixMilli()),
					CameraName: c.config.Name,
					Label:      obj.Label,
					Score:      obj.Score,
					Box:        obj.Box,
					Timestamp:  time.Now(),
				}
			}
		}

		// Emit end events for deleted tracks
		for _, obj := range c.tracker.DeletedTracks() {
			delete(c.confirmedTracks, obj.TrackID)
			c.events <- Event{
				ID:         fmt.Sprintf("%s-t%d-end-%d", c.config.Name, obj.TrackID, time.Now().UnixMilli()),
				CameraName: c.config.Name,
				Label:      obj.Label,
				Score:      obj.Score,
				Box:        obj.Box,
				Timestamp:  time.Now(),
			}
		}
	}
}

// IsOnline returns true if the camera's detection RTSP source is connected.
func (c *Camera) IsOnline() bool {
	if src := c.hub.Get(c.config.URL); src != nil {
		return src.Connected()
	}
	return false
}

// HasMotion returns true if motion was detected within the last 5 seconds.
func (c *Camera) HasMotion() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.lastMotion.IsZero() && time.Since(c.lastMotion) < 5*time.Second
}

// Status returns the current status of the camera.
func (c *Camera) Status() CameraStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	online := false
	if src := c.hub.Get(c.config.URL); src != nil {
		online = src.Connected()
	}
	return CameraStatus{
		Name:      c.config.Name,
		Online:    online,
		HasMotion: !c.lastMotion.IsZero() && time.Since(c.lastMotion) < 5*time.Second,
		LastFrame: c.lastFrameTime,
	}
}

// SnapshotRGB24 copies the raw RGB24 frame into dst and returns dimensions.
func (c *Camera) SnapshotRGB24(dst []byte) (w, h int, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rawFrame == nil {
		return 0, 0, false
	}
	needed := len(c.rawFrame)
	if len(dst) < needed {
		return 0, 0, false
	}
	copy(dst, c.rawFrame)
	return c.frameW, c.frameH, true
}

// FrameSize returns the expected RGB24 frame size based on detect config.
func (c *Camera) FrameSize() int {
	return c.config.Detect.Width * c.config.Detect.Height * 3
}

func rawToRGBA(data []byte, w, h int) *image.RGBA {
	n := w * h
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pix := img.Pix
	for i := range n {
		si := i * 3
		di := i * 4
		pix[di+0] = data[si+0]
		pix[di+1] = data[si+1]
		pix[di+2] = data[si+2]
		pix[di+3] = 255
	}
	return img
}
