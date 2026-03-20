package camera

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/detect"
)

// Event represents a detected object event from a camera.
type Event struct {
	ID         string    `json:"id"`
	CameraName string    `json:"camera"`
	Label      string    `json:"label"`
	Score      float32   `json:"score"`
	Box        [4]int    `json:"box"` // x1, y1, x2, y2
	Timestamp  time.Time `json:"timestamp"`
	SnapshotPath string  `json:"snapshot_path,omitempty"`
	ClipPath   string    `json:"clip_path,omitempty"`
}

// Camera manages a single RTSP camera stream.
type Camera struct {
	config         config.CameraConfig
	detector       *detect.Detector
	tracker        *detect.Tracker
	motionDetector *detect.MotionDetector
	events         chan<- Event
	hwaccel        *HWAccel

	mu              sync.RWMutex
	rawFrame        []byte // RGB24 frame data, guarded by mu
	frameW, frameH  int
	lastMotion      time.Time
	lastFrameTime   time.Time
	confirmedTracks map[int]bool
}

// CameraStatus represents the current status of a camera.
type CameraStatus struct {
	Name      string `json:"name"`
	Online    bool   `json:"online"`
	HasMotion bool   `json:"has_motion"`
	LastFrame time.Time `json:"last_frame"`
}

func NewCamera(cfg config.CameraConfig, detector *detect.Detector, events chan<- Event, hwaccel *HWAccel) *Camera {
	return &Camera{
		config:          cfg,
		detector:        detector,
		tracker:         detect.NewTracker(30, 3),
		motionDetector:  detect.NewMotionDetector(25, 200, 0.05),
		events:          events,
		hwaccel:         hwaccel,
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
// Allocates only when called (typically by the API), not on every frame.
func (c *Camera) LastSnapshot() *image.RGBA {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rawFrame == nil {
		return nil
	}
	return rawToRGBA(c.rawFrame, c.frameW, c.frameH)
}

// Start begins reading frames from the RTSP stream.
func (c *Camera) Start(ctx context.Context) {
	slog.Info("starting camera", "name", c.config.Name, "url", c.config.URL)

	go c.readFrames(ctx)
}

// readFrames connects to the RTSP stream via ffmpeg and decodes frames.
func (c *Camera) readFrames(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := c.runFFmpeg(ctx); err != nil {
			slog.Error("ffmpeg stream error, reconnecting",
				"camera", c.config.Name,
				"error", err,
			)
			// Wait before reconnecting
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// runFFmpeg spawns an ffmpeg process that decodes RTSP to raw frames on stdout.
func (c *Camera) runFFmpeg(ctx context.Context) error {
	w := c.config.Detect.Width
	h := c.config.Detect.Height
	fps := c.config.Detect.FPS

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-rtsp_transport", "tcp",
	}
	args = append(args, c.hwaccel.FFmpegArgs()...)
	args = append(args,
		"-i", c.config.URL,
		"-vf", fmt.Sprintf("fps=%d,scale=%d:%d", fps, w, h),
		"-pix_fmt", "rgb24",
		"-f", "rawvideo",
		"-",
	)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	frameSize := w * h * 3 // RGB24
	buf := make([]byte, frameSize)

	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		default:
		}

		n, err := readFull(stdout, buf)
		if err != nil || n != frameSize {
			_ = cmd.Process.Kill()
			return fmt.Errorf("read frame: %w (got %d bytes)", err, n)
		}

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

		// Contour-based motion detection
		motionRegions := c.motionDetector.Detect(buf, w, h)
		if len(motionRegions) > 0 {
			c.mu.Lock()
			c.lastMotion = time.Now()
			c.mu.Unlock()

			// Run object detection directly from RGB24, skipping RGBA conversion.
			// The motion regions tell us WHERE motion is, but the YOLO model
			// expects a full frame (it handles its own letterboxing/scaling).
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
}

// IsOnline returns true if a frame was received within the last 10 seconds.
func (c *Camera) IsOnline() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.lastFrameTime.IsZero() && time.Since(c.lastFrameTime) < 10*time.Second
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
	return CameraStatus{
		Name:      c.config.Name,
		Online:    !c.lastFrameTime.IsZero() && time.Since(c.lastFrameTime) < 10*time.Second,
		HasMotion: !c.lastMotion.IsZero() && time.Since(c.lastMotion) < 5*time.Second,
		LastFrame: c.lastFrameTime,
	}
}

// SnapshotRGB24 copies the raw RGB24 frame into dst and returns dimensions.
// Returns false if no frame is available. dst must be large enough.
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

func readFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
