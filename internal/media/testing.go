package media

import "image"

// NewTestSnapshotConsumer returns a SnapshotConsumer whose LastFrame reports the
// given frame, with no RTP decoder and no background decode goroutine. It lets
// tests stand in a camera's full-resolution snapshot source without RTSP or
// OpenH264 wiring.
func NewTestSnapshotConsumer(frame *image.RGBA) *SnapshotConsumer {
	return &SnapshotConsumer{lastFrame: frame}
}
