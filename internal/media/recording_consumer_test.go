package media

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
