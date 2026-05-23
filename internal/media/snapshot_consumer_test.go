package media

import (
	"image"
	"testing"
	"time"
)

// TestSnapshotConsumer_DoubleCloseIsSafe verifies Close can be called more than
// once without panicking on "close of closed channel". A consumer may be closed
// both by an explicit teardown and by a supervising shutdown path.
func TestSnapshotConsumer_DoubleCloseIsSafe(t *testing.T) {
	sc := &SnapshotConsumer{
		decodeCh: make(chan []byte, 1),
		done:     make(chan struct{}),
	}

	sc.Close()
	sc.Close() // must not panic
}

// TestSnapshotConsumer_LastFrameAfterClose verifies the accessor still works
// after close (returns the cached frame, or nil if none).
func TestSnapshotConsumer_LastFrameAfterClose(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	sc := &SnapshotConsumer{
		decodeCh:  make(chan []byte, 1),
		done:      make(chan struct{}),
		lastFrame: frame,
		lastTime:  time.Now(),
	}
	sc.Close()
	if got := sc.LastFrame(); got != frame {
		t.Errorf("LastFrame after close = %v, want cached frame", got)
	}
}
