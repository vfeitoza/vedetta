package stream

import (
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// A batch carrying non-zero composition offsets comes from a B-frame stream.
// Its per-sample Duration values ARE the decode-order DTS deltas, and the
// PTSOffsets were computed against exactly those deltas. Rewriting the
// durations to a uniform target desynchronizes the implied DTS timeline from
// the offsets, so the browser reconstructs wrong presentation times and the
// B-frame fix is silently undone. flushVideoLocked must leave such durations
// untouched.
func TestFlushVideoPreservesDTSDurationsForBFrameBatch(t *testing.T) {
	samples := []*fmp4.Sample{
		{Duration: 3000, PTSOffset: 0, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}},
		{Duration: 6000, PTSOffset: 3000, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a}},
		{Duration: 1500, PTSOffset: -1500, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x01, 0x42}},
	}
	want := []uint32{3000, 6000, 1500}

	mc := &mseConsumer{pendingVideo: samples, pendingVideoTicks: 10500}
	mc.flushVideoLocked()

	for i, s := range samples {
		if s.Duration != want[i] {
			t.Fatalf("sample %d duration = %d, want %d (B-frame DTS durations must survive flush)", i, s.Duration, want[i])
		}
	}
}

// For a B-frame-free batch (all composition offsets zero) the uniform
// smoothing is still desirable: it evens out RTP jitter without corrupting
// timing, since PTS == DTS so the implied timeline stays correct regardless
// of how the fragment total is distributed across samples.
func TestFlushVideoStillNormalizesBFrameFreeBatch(t *testing.T) {
	samples := []*fmp4.Sample{
		{Duration: 3100, PTSOffset: 0, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}},
		{Duration: 2900, PTSOffset: 0, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a}},
		{Duration: 3000, PTSOffset: 0, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x42}},
	}

	mc := &mseConsumer{pendingVideo: samples, pendingVideoTicks: 9000}
	mc.flushVideoLocked()

	for i, s := range samples {
		if s.Duration != 3000 {
			t.Fatalf("sample %d duration = %d, want 3000 (jitter smoothing must still apply for B-frame-free streams)", i, s.Duration)
		}
	}
}
