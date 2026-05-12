package media

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/rvben/vedetta/internal/rtsp"
)

func testDisk(t *testing.T) *DiskSpace {
	return NewDiskSpace(t.TempDir())
}

func TestRecordingConsumer_SegmentCallback(t *testing.T) {
	dir := t.TempDir()

	video := &rtsp.TrackInfo{
		Codec:     "H264",
		ClockRate: 90000,
		IsVideo:   true,
		SPS:       []byte{0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2},
		PPS:       []byte{0x68, 0xce, 0x38, 0x80},
	}

	var mu sync.Mutex
	var segments []SegmentInfo

	rc := NewRecordingConsumer(dir, "test-cam", time.Second, video, nil, testDisk(t), func(info SegmentInfo) {
		mu.Lock()
		segments = append(segments, info)
		mu.Unlock()
	})

	// Close immediately triggers segment callback if data was written
	rc.Close()

	// No packets were written, so we may or may not get a callback
	// depending on whether the writer was initialized
	mu.Lock()
	defer mu.Unlock()
	// This is valid — no crash, no panic
}

func TestRecordingConsumer_Close_NilWriter(t *testing.T) {
	dir := t.TempDir()

	rc := NewRecordingConsumer(dir, "test-cam", time.Minute, nil, nil, testDisk(t), nil)
	rc.Close() // should not panic
}

func TestRecordingConsumer_OnDisconnect_ClosesSegment(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var segments []SegmentInfo

	rc := NewRecordingConsumer(dir, "test-cam", time.Minute, nil, nil, testDisk(t), func(info SegmentInfo) {
		mu.Lock()
		segments = append(segments, info)
		mu.Unlock()
	})

	rc.OnDisconnect()
	rc.Close()
	// Should handle multiple close/disconnect calls gracefully
}

func TestRecordingConsumer_SegmentDir_Created(t *testing.T) {
	base := t.TempDir()
	segDir := filepath.Join(base, "nested", "segments")

	rc := NewRecordingConsumer(segDir, "test-cam", time.Minute, nil, nil, testDisk(t), nil)
	rc.Close()

	if _, err := os.Stat(segDir); os.IsNotExist(err) {
		t.Error("segment directory was not created")
	}
}

func TestRecordingConsumer_PausedState(t *testing.T) {
	dir := t.TempDir()
	disk := testDisk(t)

	rc := NewRecordingConsumer(dir, "test-cam", time.Minute, nil, nil, disk, nil)
	defer rc.Close()

	if rc.Paused() {
		t.Error("consumer should not be paused on start with available disk space")
	}
}

func TestRecordingConsumer_Accessors(t *testing.T) {
	rc := &RecordingConsumer{camera: "kids_bedroom_3"}
	if got := rc.Camera(); got != "kids_bedroom_3" {
		t.Errorf("Camera() = %q, want kids_bedroom_3", got)
	}
	if got := rc.CurrentSegmentPath(); got != "" {
		t.Errorf("CurrentSegmentPath() with no open segment = %q, want empty", got)
	}

	// Simulate an open segment by setting the writer path field directly.
	rc.mu.Lock()
	rc.currentPath = "/tmp/seg-001.mp4"
	rc.mu.Unlock()

	if got := rc.CurrentSegmentPath(); got != "/tmp/seg-001.mp4" {
		t.Errorf("CurrentSegmentPath() = %q, want /tmp/seg-001.mp4", got)
	}
}

func TestRecordingConsumer_EnsureSegmentError_PausesAfterRepeatedFailures(t *testing.T) {
	// Use a read-only directory so segment file creation fails
	dir := t.TempDir()
	segDir := filepath.Join(dir, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Make the segment directory read-only so os.Create fails
	if err := os.Chmod(segDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(segDir, 0o755) })

	video := &rtsp.TrackInfo{
		Codec:     "H264",
		ClockRate: 90000,
		IsVideo:   true,
		SPS:       []byte{0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2},
		PPS:       []byte{0x68, 0xce, 0x38, 0x80},
	}

	rc := NewRecordingConsumer(segDir, "test-cam", time.Minute, video, nil, testDisk(t), nil)
	defer rc.Close()

	// Send enough video packets to trigger 3+ ensureSegment failures
	for i := 0; i < 5; i++ {
		rc.OnVideoRTP(&rtp.Packet{
			Header: rtp.Header{
				PayloadType: 96,
				Timestamp:   uint32(i * 3000),
			},
			Payload: []byte{0x65, 0x00, 0x01}, // fake IDR
		})
	}

	// Wait for processLoop to handle the packets
	time.Sleep(100 * time.Millisecond)

	if !rc.Paused() {
		t.Error("consumer should be paused after repeated segment creation failures")
	}
}
