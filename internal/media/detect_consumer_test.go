package media

import (
	"strings"
	"testing"

	"github.com/rvben/vedetta/internal/metrics"
)

// dispatchFrame must deliver a decoded frame when the detection channel has
// room and drop-and-count it when the channel is full, so decoding never blocks
// on a busy detector. The drop counter is labelled by camera.
func TestDetectConsumerDispatchFrameCountsDrops(t *testing.T) {
	metrics.ResetForTest()
	t.Cleanup(metrics.ResetForTest)

	dc := &DetectConsumer{camera: "garage", frameCh: make(chan RawFrame, 1)}

	// First frame fits the buffered channel: delivered, not dropped.
	dc.dispatchFrame(RawFrame{Width: 1, Height: 1})
	// Channel now full; the next two frames must be dropped and counted.
	dc.dispatchFrame(RawFrame{Width: 1, Height: 1})
	dc.dispatchFrame(RawFrame{Width: 1, Height: 1})

	var b strings.Builder
	metrics.WriteProm(&b)
	out := b.String()

	if !strings.Contains(out, `vedetta_detect_input_dropped_total{camera="garage"} 2`) {
		t.Errorf("expected 2 dropped frames for garage:\n%s", out)
	}
	if len(dc.frameCh) != 1 {
		t.Errorf("expected 1 frame delivered to channel, got %d", len(dc.frameCh))
	}
}
