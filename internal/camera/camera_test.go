package camera

import (
	"testing"

	"github.com/rvben/watchpost/internal/config"
)

func TestSnapshotRGB24_NoFrame(t *testing.T) {
	cam := NewCamera(config.CameraConfig{
		Name: "test",
		Detect: config.StreamConfig{Width: 64, Height: 64, FPS: 5},
	}, nil, make(chan<- Event, 1), nil)

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
	}, nil, make(chan<- Event, 1), nil)

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
	}, nil, make(chan<- Event, 1), nil)

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
	}, nil, make(chan<- Event, 1), nil)

	expected := 320 * 240 * 3
	if got := cam.FrameSize(); got != expected {
		t.Fatalf("FrameSize() = %d, want %d", got, expected)
	}
}
