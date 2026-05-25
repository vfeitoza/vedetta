package api

import (
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func TestDetectionHub_SubscribePublishUnsubscribe(t *testing.T) {
	hub := newDetectionHub()

	sub := hub.Subscribe("cam1")
	defer hub.Unsubscribe(sub)

	frame := camera.DetectionFrame{
		Camera:    "cam1",
		Timestamp: time.Now(),
		Boxes:     []camera.DetectionBox{{Label: "person", Score: 0.9}},
	}
	hub.Publish(frame)

	select {
	case got := <-sub.ch:
		if got.Camera != "cam1" || len(got.Boxes) != 1 || got.Boxes[0].Label != "person" {
			t.Fatalf("unexpected frame received: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive published frame")
	}
}

func TestDetectionHub_PublishToOtherCameraIsIgnored(t *testing.T) {
	hub := newDetectionHub()
	sub := hub.Subscribe("cam1")
	defer hub.Unsubscribe(sub)

	hub.Publish(camera.DetectionFrame{Camera: "cam2"})

	select {
	case got := <-sub.ch:
		t.Fatalf("received frame for wrong camera: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDetectionHub_SlowSubscriberDropsFrames(t *testing.T) {
	hub := newDetectionHub()
	sub := hub.Subscribe("cam1")
	defer hub.Unsubscribe(sub)

	// Buffer is 4; publish 100 frames and verify Publish never blocks.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			hub.Publish(camera.DetectionFrame{Camera: "cam1"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish appears to be blocking on a slow subscriber")
	}

	// Buffer holds at most 4 frames; the other 96 were dropped and must be
	// counted so silent overlay degradation is visible on /metrics.
	if got := hub.DroppedFrames(); got != 96 {
		t.Errorf("DroppedFrames() = %d, want 96 (100 published, 4 buffered)", got)
	}

	// Buffer holds at most 4 dropped frames; drain what's there.
	drained := 0
	for {
		select {
		case <-sub.ch:
			drained++
		case <-time.After(20 * time.Millisecond):
			if drained == 0 {
				t.Fatal("expected at least one frame to be buffered")
			}
			if drained > 4 {
				t.Fatalf("buffer leaked, drained %d frames", drained)
			}
			return
		}
	}
}

func TestDetectionHub_UnsubscribeRemovesFromMap(t *testing.T) {
	hub := newDetectionHub()
	sub := hub.Subscribe("cam1")

	hub.mu.RLock()
	if len(hub.subs["cam1"]) != 1 {
		hub.mu.RUnlock()
		t.Fatal("expected 1 subscriber after Subscribe")
	}
	hub.mu.RUnlock()

	hub.Unsubscribe(sub)

	hub.mu.RLock()
	if _, ok := hub.subs["cam1"]; ok {
		hub.mu.RUnlock()
		t.Fatal("empty camera entry should be removed after last Unsubscribe")
	}
	hub.mu.RUnlock()

	// Subsequent publishes should not panic on closed channel.
	hub.Publish(camera.DetectionFrame{Camera: "cam1"})
}
