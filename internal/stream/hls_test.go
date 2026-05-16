package stream

import (
	"strings"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// addVideoSample stages one fake video sample on the segment under
// construction, mirroring what OnVideoRTP does once a packet's duration is
// known. The payload bytes are arbitrary: closeSegmentLocked only marshals
// them into the fragment, it does not parse H.264.
func addVideoSample(c *hlsConsumer, durTicks uint32) {
	c.segVideo = append(c.segVideo, &fmp4.Sample{
		Duration: durTicks,
		Payload:  []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	})
	c.segVideoTicks += durTicks
}

// closeOneSecondSegment fills a one-second video GOP and closes it through
// the production segmentation path.
func closeOneSecondSegment(c *hlsConsumer) {
	addVideoSample(c, hlsTargetSegmentTicks)
	c.closeSegmentLocked()
}

func TestHLSPlaylistEmptyUntilFirstSegment(t *testing.T) {
	c := newHLSConsumer(nil, nil)
	if _, ok := c.playlist(); ok {
		t.Fatal("playlist must report not-ready while the ring is empty")
	}
	if _, ok := c.initSeg(); ok {
		t.Fatal("init segment must report not-ready before it is built")
	}
}

func TestHLSPlaylistFormat(t *testing.T) {
	c := newHLSConsumer(nil, nil)
	c.initSegment = []byte("INIT")
	c.ring = []hlsSegment{
		{id: 4, data: []byte("a"), duration: 1.0},
		{id: 5, data: []byte("b"), duration: 2.0, disc: true},
		{id: 6, data: []byte("c"), duration: 1.5},
	}

	pl, ok := c.playlist()
	if !ok {
		t.Fatal("playlist must be ready once the ring has segments")
	}

	mustContain := []string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		"#EXT-X-TARGETDURATION:2", // ceil(max duration 2.0)
		"#EXT-X-MEDIA-SEQUENCE:4", // first segment id in the window
		"#EXT-X-MAP:URI=\"live/init.mp4\"",
		"#EXT-X-DISCONTINUITY", // before segment 5
		"#EXTINF:1.000000,",
		"live/4",
		"#EXTINF:2.000000,",
		"live/5",
		"#EXTINF:1.500000,",
		"live/6",
	}
	for _, want := range mustContain {
		if !strings.Contains(pl, want) {
			t.Errorf("playlist missing %q\n---\n%s", want, pl)
		}
	}

	// A live playlist must never terminate.
	if strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Error("live playlist must not contain #EXT-X-ENDLIST")
	}

	// The discontinuity tag belongs immediately before segment 5, not 4 or 6.
	discIdx := strings.Index(pl, "#EXT-X-DISCONTINUITY")
	seg5Idx := strings.Index(pl, "live/5")
	seg4Idx := strings.Index(pl, "live/4")
	if !(seg4Idx < discIdx && discIdx < seg5Idx) {
		t.Errorf("discontinuity tag is not positioned before segment 5\n---\n%s", pl)
	}
}

func TestHLSSegmentRotationIsMonotonicAndBounded(t *testing.T) {
	c := newHLSConsumer(nil, nil)

	const total = hlsWindowSegments + 5
	for i := 0; i < total; i++ {
		closeOneSecondSegment(c)
	}

	if len(c.ring) != hlsWindowSegments {
		t.Fatalf("ring not bounded: got %d segments, want %d", len(c.ring), hlsWindowSegments)
	}

	// IDs strictly increase by one and never reset across rotations.
	for i := 1; i < len(c.ring); i++ {
		if c.ring[i].id != c.ring[i-1].id+1 {
			t.Fatalf("segment ids not contiguous/monotonic: %d then %d", c.ring[i-1].id, c.ring[i].id)
		}
	}

	// The oldest retained id is total-window; nextSegID is total.
	wantFirst := uint64(total - hlsWindowSegments)
	if c.ring[0].id != wantFirst {
		t.Fatalf("media-sequence did not advance: ring[0].id=%d want %d", c.ring[0].id, wantFirst)
	}
	if c.nextSegID != uint64(total) {
		t.Fatalf("nextSegID=%d want %d", c.nextSegID, total)
	}
	if c.moofSeq != uint32(total) {
		t.Fatalf("moofSeq=%d want %d (one fragment per segment)", c.moofSeq, total)
	}

	// MEDIA-SEQUENCE in the rendered playlist tracks the oldest retained id.
	pl, ok := c.playlist()
	if !ok {
		t.Fatal("playlist must be ready")
	}
	if !strings.Contains(pl, "#EXT-X-MEDIA-SEQUENCE:"+itoa(wantFirst)) {
		t.Fatalf("playlist MEDIA-SEQUENCE not %d\n---\n%s", wantFirst, pl)
	}

	// Running video DTS is the sum of every emitted segment's ticks, so
	// fragment BaseTime stays continuous across rotation.
	if c.videoDTS != uint64(total)*hlsTargetSegmentTicks {
		t.Fatalf("videoDTS=%d want %d", c.videoDTS, uint64(total)*hlsTargetSegmentTicks)
	}
}

func TestHLSDiscontinuityFlagConsumedOnce(t *testing.T) {
	c := newHLSConsumer(nil, nil)

	// Simulate the SPS-change path setting pendingDisc before the next cut.
	c.pendingDisc = true
	closeOneSecondSegment(c)
	closeOneSecondSegment(c)

	if !c.ring[0].disc {
		t.Error("first segment after an SPS change must carry the discontinuity flag")
	}
	if c.ring[1].disc {
		t.Error("discontinuity flag must apply to exactly one segment")
	}
	if c.pendingDisc {
		t.Error("pendingDisc must be cleared after it is consumed")
	}
}

func TestHLSSegmentLookupAndEviction(t *testing.T) {
	c := newHLSConsumer(nil, nil)

	const total = hlsWindowSegments + 3
	for i := 0; i < total; i++ {
		closeOneSecondSegment(c)
	}

	// A segment still inside the window resolves to bytes.
	liveID := c.ring[len(c.ring)-1].id
	if data, ok := c.segment(liveID); !ok || len(data) == 0 {
		t.Fatalf("expected segment %d to be served, ok=%v len=%d", liveID, ok, len(data))
	}

	// An evicted segment is gone, not stale.
	if _, ok := c.segment(0); ok {
		t.Error("evicted segment 0 must not be served")
	}
	// An id that was never produced is absent.
	if _, ok := c.segment(99999); ok {
		t.Error("unknown segment id must not be served")
	}
}

func TestHLSInitSegmentReturnsDefensiveCopy(t *testing.T) {
	c := newHLSConsumer(nil, nil)
	c.initSegment = []byte{0x01, 0x02, 0x03}

	got, ok := c.initSeg()
	if !ok {
		t.Fatal("init segment must be served once present")
	}
	got[0] = 0xFF
	if c.initSegment[0] != 0x01 {
		t.Error("initSeg must hand out a copy, not the backing slice")
	}
}

// itoa avoids pulling strconv into the test for a single conversion.
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
