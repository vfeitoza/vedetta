package stream

import (
	"bytes"
	"testing"
)

// A real H.264 stream whose encoder reorders frames (B-frames): the RTP
// timestamp is the presentation time (PTS), but frames arrive in decode
// order, so PTS != DTS. These access units and their library-derived
// PTS/DTS come from mediacommon's own DTSExtractor fixture, so the
// expectations are ground truth, not hand-computed.
//
// Decode-order sequence (offsets from an arbitrary base):
//
//	#  au                              dts     pts     PTS-DTS
//	0  SPS+IDR                            0       0        0
//	1  P  0x41 0x9a 0x21 ...           3000    3000        0
//	2  P  0x41 0x9a 0x42 ...           6000    6000        0
//	3  P  0x41 0x9a 0x63 ...           9000    9000        0
//	4  P  0x41 0x9a 0x86 ...           9090   18000     8910
//	5  P  0x41 0x9e 0xa5 ...          12045   15000     2955
//	6  B  0x01 0x9e 0xc4 ...          12135   12000      -135
//	7  P  0x41 0x9a 0xc8 ...          15101   24000     8899
//	8  IDR                            18067   24000     5933
const ptsBase = 56890

type bframeAU struct {
	au  [][]byte
	dts int64
	pts int64
}

var bframeSequence = []bframeAU{
	{[][]byte{
		{0x67, 0x64, 0x00, 0x28, 0xac, 0xd9, 0x40, 0x78, 0x02, 0x27, 0xe5, 0x84,
			0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc6, 0x58},
		{0x65, 0x88, 0x84, 0x00, 0x33, 0xff},
	}, ptsBase + 0, ptsBase + 0},
	{[][]byte{{0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff}}, ptsBase + 3000, ptsBase + 3000},
	{[][]byte{{0x41, 0x9a, 0x42, 0x3c, 0x21, 0x93}}, ptsBase + 6000, ptsBase + 6000},
	{[][]byte{{0x41, 0x9a, 0x63, 0x49, 0xe1, 0x0f}}, ptsBase + 9000, ptsBase + 9000},
	{[][]byte{{0x41, 0x9a, 0x86, 0x49, 0xe1, 0x0f}}, ptsBase + 9090, ptsBase + 18000},
	{[][]byte{{0x41, 0x9e, 0xa5, 0x42, 0x7f, 0xf9}}, ptsBase + 12045, ptsBase + 15000},
	{[][]byte{{0x01, 0x9e, 0xc4, 0x69, 0x13, 0xff}}, ptsBase + 12135, ptsBase + 12000},
	{[][]byte{{0x41, 0x9a, 0xc8, 0x4b, 0xa8, 0x42}}, ptsBase + 15101, ptsBase + 24000},
	{[][]byte{{0x65, 0x88, 0x84, 0x00, 0x33, 0xff}}, ptsBase + 18067, ptsBase + 24000},
}

// The muxer used to stamp every fMP4 sample with FillH264(0, au) and derive
// each sample's duration from the PTS delta in decode order. For a B-frame
// stream PTS is non-monotonic in decode order, so the duration delta wraps
// negative or huge and gets clamped to a flat 30fps guess, and the zero
// composition-time offset makes AVPlayer present frames in the wrong order -
// it can only render the IDR/anchor frames and visibly jumps once per
// segment. The timer must instead carry the true PTS-DTS composition offset
// and decode-order (DTS-delta) durations so the player can reorder.
func TestH264SampleTimerCarriesBFrameCompositionOffsets(t *testing.T) {
	timer := newH264SampleTimer("test")

	type got struct {
		ptsOffset int32
		duration  uint32
	}
	var finalized []got

	for _, s := range bframeSequence {
		fin, dur, ok := timer.push(s.au, uint32(s.pts))
		if ok {
			finalized = append(finalized, got{fin.PTSOffset, dur})
		}
	}

	// Every AU except the last (still in flight) is finalized in decode order.
	if len(finalized) != len(bframeSequence)-1 {
		t.Fatalf("finalized %d samples, want %d", len(finalized), len(bframeSequence)-1)
	}

	for i, f := range finalized {
		wantOffset := int32(bframeSequence[i].pts - bframeSequence[i].dts)
		if f.ptsOffset != wantOffset {
			t.Errorf("sample %d PTSOffset = %d, want %d (PTS %d - DTS %d); "+
				"a zero offset here is the bug that makes garage render only keyframes",
				i, f.ptsOffset, wantOffset,
				bframeSequence[i].pts-ptsBase, bframeSequence[i].dts-ptsBase)
		}
		// Decode duration is the DTS gap to the next AU - always positive,
		// never the clamped fallback for the reordered frames.
		wantDur := uint32(bframeSequence[i+1].dts - bframeSequence[i].dts)
		if f.duration != wantDur {
			t.Errorf("sample %d duration = %d, want %d (DTS-delta in decode order)",
				i, f.duration, wantDur)
		}
	}

	// At least one reordered frame must carry a non-zero offset, otherwise
	// the test would also pass against the old FillH264(0, au) code.
	sawNonZero := false
	for _, f := range finalized {
		if f.ptsOffset != 0 {
			sawNonZero = true
			break
		}
	}
	if !sawNonZero {
		t.Fatal("no sample carried a non-zero composition offset; B-frame " +
			"reordering is not being represented at all")
	}
}

// Many RTSP cameras advertise SPS/PPS only in the SDP and never repeat the
// SPS in-band. The DTS extractor learns reordering depth solely from an
// in-band SPS, so without the out-of-band parameter sets it never
// initializes, every Extract fails, the timer falls back to PTS, and a
// B-frame stream silently loses its composition offsets again. The timer
// must accept the SDP parameter sets and seed the extractor with them.
func TestH264SampleTimerUsesSDPParameterSets(t *testing.T) {
	sps := bframeSequence[0].au[0]
	pps := []byte{0x68, 0xee, 0x3c, 0x80}

	timer := newH264SampleTimer("test")
	timer.setParameterSets(sps, pps)

	// SDP-only camera: the first keyframe AU carries the IDR slice but no
	// in-band SPS; later AUs are bare slices. PTS/DTS are unchanged because
	// the SPS bytes are identical, only the delivery differs.
	seq := make([]bframeAU, len(bframeSequence))
	copy(seq, bframeSequence)
	seq[0] = bframeAU{[][]byte{bframeSequence[0].au[1]}, ptsBase, ptsBase}

	var offsets []int32
	for _, s := range seq {
		if fin, _, ok := timer.push(s.au, uint32(s.pts)); ok {
			offsets = append(offsets, fin.PTSOffset)
		}
	}

	sawNonZero := false
	for _, o := range offsets {
		if o != 0 {
			sawNonZero = true
			break
		}
	}
	if !sawNonZero {
		t.Fatal("no composition offset survived with SDP-only parameter sets; " +
			"the DTS extractor was never seeded and fell back to PTS")
	}
}

// After an SPS change the consumer resets the timer and re-seeds it with the
// new parameter sets. A later call must replace the cached SPS/PPS, otherwise
// a keyframe that omits an in-band SPS would be prefixed with the stale SPS -
// corrupting the DTS extractor and the emitted sample.
func TestSetParameterSetsReplacesStaleSets(t *testing.T) {
	timer := newH264SampleTimer("test")

	staleSPS := []byte{0x67, 0xde, 0xad}
	freshSPS := bframeSequence[0].au[0]
	timer.setParameterSets(staleSPS, []byte{0x68, 0x01})
	timer.setParameterSets(freshSPS, []byte{0x68, 0x02})

	idrOnly := [][]byte{{0x65, 0x88, 0x84, 0x00, 0x33, 0xff}}
	got := timer.withParameterSets(idrOnly)

	if len(got) != 3 {
		t.Fatalf("expected [SPS, PPS, IDR], got %d NALUs", len(got))
	}
	if !bytes.Equal(got[0], freshSPS) {
		t.Fatalf("keyframe prefixed with stale SPS %x, want fresh %x", got[0], freshSPS)
	}
	if !bytes.Equal(got[1], []byte{0x68, 0x02}) {
		t.Fatalf("keyframe prefixed with stale PPS %x, want fresh", got[1])
	}
}

// A B-frame-free stream (Main/Baseline, e.g. front_door) has PTS == DTS for
// every frame. Those streams play smoothly today and must keep producing a
// zero composition offset so the fix is provably a no-op for them.
func TestH264SampleTimerBFrameFreeStreamStaysZeroOffset(t *testing.T) {
	timer := newH264SampleTimer("test")

	// SPS+IDR then a run of P-frames at a steady 30fps, PTS == DTS.
	idr := [][]byte{
		{0x67, 0x64, 0x00, 0x28, 0xac, 0xd9, 0x40, 0x78, 0x02, 0x27, 0xe5, 0x84,
			0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc6, 0x58},
		{0x65, 0x88, 0x84, 0x00, 0x33, 0xff},
	}
	ps := [][][]byte{
		{{0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff}},
		{{0x41, 0x9a, 0x42, 0x3c, 0x21, 0x93}},
		{{0x41, 0x9a, 0x63, 0x49, 0xe1, 0x0f}},
	}

	var pts uint32 = 9000
	if fin, _, ok := timer.push(idr, pts); ok {
		t.Fatalf("first AU must not finalize a prior sample, got %+v", fin)
	}
	for _, p := range ps {
		pts += 3000
		fin, dur, ok := timer.push(p, pts)
		if !ok {
			t.Fatal("expected the previous sample to finalize")
		}
		if fin.PTSOffset != 0 {
			t.Errorf("B-frame-free sample got PTSOffset %d, want 0", fin.PTSOffset)
		}
		if dur != 3000 {
			t.Errorf("steady 30fps duration = %d, want 3000", dur)
		}
	}
}
