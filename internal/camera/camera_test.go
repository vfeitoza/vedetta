package camera

import (
	"context"
	"testing"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/rtsp"
)

func TestSnapshotRGB24_NoFrame(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		Detect: config.StreamConfig{Width: 64, Height: 64, FPS: 5},
	}, nil, make(chan<- Event, 1), nil, "", 85)

	dst := make([]byte, 64*64*3)
	_, _, ok := cam.SnapshotRGB24(dst)
	if ok {
		t.Fatal("expected ok=false when no frame available")
	}
}

func TestSnapshotRGB24_CopiesFrame(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		Detect: config.StreamConfig{Width: 4, Height: 4, FPS: 5},
	}, nil, make(chan<- Event, 1), nil, "", 85)

	frameSize := 4 * 4 * 3
	frame := make([]byte, frameSize)
	for i := range frame {
		frame[i] = byte(i % 256)
	}

	cam.mu.Lock()
	cam.rawFrame = make([]byte, frameSize)
	copy(cam.rawFrame, frame)
	cam.frameW = 4
	cam.frameH = 4
	cam.mu.Unlock()

	dst := make([]byte, frameSize)
	w, h, ok := cam.SnapshotRGB24(dst)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if w != 4 || h != 4 {
		t.Fatalf("expected 4x4, got %dx%d", w, h)
	}
	for i := range frame {
		if dst[i] != frame[i] {
			t.Fatalf("byte %d: got %d, want %d", i, dst[i], frame[i])
		}
	}
}

func TestSnapshotRGB24_DstTooSmall(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		Detect: config.StreamConfig{Width: 4, Height: 4, FPS: 5},
	}, nil, make(chan<- Event, 1), nil, "", 85)

	frameSize := 4 * 4 * 3
	cam.mu.Lock()
	cam.rawFrame = make([]byte, frameSize)
	cam.frameW = 4
	cam.frameH = 4
	cam.mu.Unlock()

	dst := make([]byte, 10) // too small
	_, _, ok := cam.SnapshotRGB24(dst)
	if ok {
		t.Fatal("expected ok=false when dst too small")
	}
}

func TestFrameSize(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		Detect: config.StreamConfig{Width: 320, Height: 240, FPS: 5},
	}, nil, make(chan<- Event, 1), nil, "", 85)

	expected := 320 * 240 * 3
	if got := cam.FrameSize(); got != expected {
		t.Fatalf("FrameSize() = %d, want %d", got, expected)
	}
}

func TestIsOnline_NoHub(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		URL:  "rtsp://localhost/test",
	}, nil, make(chan<- Event, 1), nil, "", 85)

	if cam.IsOnline() {
		t.Error("expected IsOnline=false with nil hub")
	}
}

func TestIsOnline_NoSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := rtsp.NewHub(ctx)
	defer hub.Close()

	cam := NewCamera(config.CameraConfig{
		Name: "test",
		URL:  "rtsp://localhost/test",
	}, nil, make(chan<- Event, 1), hub, "", 85)

	// No source created for this URL yet
	if cam.IsOnline() {
		t.Error("expected IsOnline=false when no source exists")
	}
}

func TestStatus_NoHub(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		URL:  "rtsp://localhost/test",
	}, nil, make(chan<- Event, 1), nil, "", 85)

	st := cam.Status()
	if st.Online {
		t.Error("expected Online=false with nil hub")
	}
	if st.Name != "test" {
		t.Errorf("Name = %q, want %q", st.Name, "test")
	}
}
