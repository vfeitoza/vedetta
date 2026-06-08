package stream

import (
	"context"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/rtsp"
)

// h264Params is a minimal SPS/PPS pair: long enough to pass the source's
// param-readiness length checks. The bytes need not form a decodable stream -
// these tests exercise consumer lifecycle, not H.264 decoding.
func h264Params() *rtsp.TrackInfo {
	return &rtsp.TrackInfo{
		Codec:   "H264",
		IsVideo: true,
		SPS:     []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:     []byte{0x68, 0xce, 0x38, 0x80},
	}
}

func consumerCount(m *HLSManager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.consumers)
}

func TestGetOrCreateReusesThenRebuildsOnSourceChange(t *testing.T) {
	hub := rtsp.NewHub(context.Background())
	const url = "rtsp://192.0.2.20:554/sub"

	src1 := rtsp.NewSource(url)
	src1.SetVideoTrack(h264Params())
	hub.SetSourceForTest(url, src1)

	m := NewHLSManager(hub)
	defer m.Close()

	c1 := m.getOrCreate(url)
	if c1 == nil {
		t.Fatal("getOrCreate returned nil")
	}
	if c1.source != src1 {
		t.Fatal("consumer must record the source it attached to")
	}
	if again := m.getOrCreate(url); again != c1 {
		t.Fatal("getOrCreate must reuse the consumer while the source is unchanged")
	}

	// Simulate a camera stop/start: the hub's source for this URL is replaced.
	src2 := rtsp.NewSource(url)
	src2.SetVideoTrack(h264Params())
	hub.SetSourceForTest(url, src2)

	c2 := m.getOrCreate(url)
	if c2 == c1 {
		t.Fatal("getOrCreate must rebuild the consumer after the source was recreated")
	}
	if c2.source != src2 {
		t.Fatal("rebuilt consumer must attach to the new source")
	}
	if consumerCount(m) != 1 {
		t.Fatalf("stale consumer must be dropped, not accumulated: count=%d", consumerCount(m))
	}
}

func TestReapIdleSkipsWarmButReapsOthers(t *testing.T) {
	m := NewHLSManager(nil) // nil hub is fine: reapIdle no longer dereferences the hub
	defer m.Close()

	const warmURL = "rtsp://192.0.2.30:554/sub"
	const coldURL = "rtsp://192.0.2.31:554/sub"
	cwarm := newHLSConsumer(nil, nil)
	ccold := newHLSConsumer(nil, nil)
	old := time.Now().Add(-time.Hour).UnixNano()
	cwarm.lastAccess.Store(old)
	ccold.lastAccess.Store(old)

	m.mu.Lock()
	m.consumers[warmURL] = cwarm
	m.consumers[coldURL] = ccold
	m.warm[warmURL] = func() {}
	m.mu.Unlock()

	m.reapIdle(time.Now())

	m.mu.Lock()
	_, warmStill := m.consumers[warmURL]
	_, coldStill := m.consumers[coldURL]
	m.mu.Unlock()
	if !warmStill {
		t.Error("a warm consumer must never be reaped, even when idle")
	}
	if coldStill {
		t.Error("a non-warm idle consumer must still be reaped")
	}
}
