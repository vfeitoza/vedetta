package media

import (
	"bytes"
	"testing"
)

// These tests exercise the pure NAL-format converters that the transcoder uses
// to move H264 payloads between AVCC (length-prefixed, fMP4 sample form) and
// Annex B (start-code-delimited, decoder/encoder form). They run without
// OpenH264 or sample recordings, so the conversion contract is verified on
// every build rather than only when a real clip happens to be present.

const (
	nalSPS   = 0x67 // nal_ref_idc=3, type=7
	nalPPS   = 0x68 // nal_ref_idc=3, type=8
	nalIDR   = 0x65 // nal_ref_idc=3, type=5
	nalSlice = 0x41 // nal_ref_idc=2, type=1 (non-IDR)
)

func annexBStartCode4(payload ...byte) []byte {
	return append([]byte{0, 0, 0, 1}, payload...)
}

func TestSplitAnnexB_FourByteStartCodes(t *testing.T) {
	data := bytes.Join([][]byte{
		annexBStartCode4(nalSPS, 0xAA),
		annexBStartCode4(nalIDR, 0xBB, 0xCC),
	}, nil)

	nals := splitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("got %d NALs, want 2", len(nals))
	}
	if !bytes.Equal(nals[0], []byte{nalSPS, 0xAA}) {
		t.Errorf("NAL 0 = %v, want [67 AA]", nals[0])
	}
	if !bytes.Equal(nals[1], []byte{nalIDR, 0xBB, 0xCC}) {
		t.Errorf("NAL 1 = %v, want [65 BB CC]", nals[1])
	}
}

func TestSplitAnnexB_ThreeByteStartCodes(t *testing.T) {
	data := bytes.Join([][]byte{
		{0, 0, 1, nalSPS, 0x11},
		{0, 0, 1, nalPPS, 0x22},
	}, nil)

	nals := splitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("got %d NALs, want 2", len(nals))
	}
	if !bytes.Equal(nals[0], []byte{nalSPS, 0x11}) || !bytes.Equal(nals[1], []byte{nalPPS, 0x22}) {
		t.Errorf("NALs = %v, want [[67 11] [68 22]]", nals)
	}
}

func TestSplitAnnexB_DropsBytesBeforeFirstStartCode(t *testing.T) {
	// Leading bytes with no start code are not part of any NAL and must be
	// discarded rather than smuggled into the first unit.
	data := append([]byte{0xDE, 0xAD}, annexBStartCode4(nalIDR, 0x01)...)
	nals := splitAnnexB(data)
	if len(nals) != 1 {
		t.Fatalf("got %d NALs, want 1", len(nals))
	}
	if !bytes.Equal(nals[0], []byte{nalIDR, 0x01}) {
		t.Errorf("NAL 0 = %v, want [65 01]", nals[0])
	}
}

func TestSplitAnnexB_Empty(t *testing.T) {
	if nals := splitAnnexB(nil); len(nals) != 0 {
		t.Errorf("splitAnnexB(nil) = %v, want empty", nals)
	}
}

func TestAvccToAnnexB_ConvertsLengthPrefixedNALs(t *testing.T) {
	// Two AVCC NALs: a 2-byte and a 3-byte unit.
	avcc := []byte{
		0, 0, 0, 2, nalSlice, 0xAA,
		0, 0, 0, 3, nalIDR, 0xBB, 0xCC,
	}
	got, err := avccToAnnexB(avcc)
	if err != nil {
		t.Fatalf("avccToAnnexB: %v", err)
	}
	want := bytes.Join([][]byte{
		annexBStartCode4(nalSlice, 0xAA),
		annexBStartCode4(nalIDR, 0xBB, 0xCC),
	}, nil)
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAvccToAnnexB_RejectsTruncatedLength(t *testing.T) {
	// Declares a 10-byte NAL but only 2 bytes follow.
	avcc := []byte{0, 0, 0, 10, nalIDR, 0x01}
	if _, err := avccToAnnexB(avcc); err == nil {
		t.Fatal("expected error for NAL length exceeding buffer")
	}
}

func TestAvccToAnnexB_RejectsEmpty(t *testing.T) {
	if _, err := avccToAnnexB(nil); err == nil {
		t.Fatal("expected error for empty AVCC payload")
	}
}

func TestAnnexBToAVCC_DropsParameterSetsAndLengthPrefixes(t *testing.T) {
	annexB := bytes.Join([][]byte{
		annexBStartCode4(nalSPS, 0x11),
		annexBStartCode4(nalPPS, 0x22),
		annexBStartCode4(nalIDR, 0xBB, 0xCC),
	}, nil)

	got, err := annexBToAVCC(annexB)
	if err != nil {
		t.Fatalf("annexBToAVCC: %v", err)
	}
	// Only the IDR survives; SPS/PPS belong in the init segment.
	want := []byte{0, 0, 0, 3, nalIDR, 0xBB, 0xCC}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAnnexBToAVCC_ErrorsWhenOnlyParameterSets(t *testing.T) {
	annexB := bytes.Join([][]byte{
		annexBStartCode4(nalSPS, 0x11),
		annexBStartCode4(nalPPS, 0x22),
	}, nil)
	if _, err := annexBToAVCC(annexB); err == nil {
		t.Fatal("expected error when payload has no slice NALs")
	}
}

func TestAvccAnnexBRoundTripForSliceNALs(t *testing.T) {
	// A non-parameter-set NAL must survive AVCC → Annex B → AVCC unchanged.
	avcc := []byte{0, 0, 0, 4, nalIDR, 0x01, 0x02, 0x03}
	annexB, err := avccToAnnexB(avcc)
	if err != nil {
		t.Fatalf("avccToAnnexB: %v", err)
	}
	back, err := annexBToAVCC(annexB)
	if err != nil {
		t.Fatalf("annexBToAVCC: %v", err)
	}
	if !bytes.Equal(back, avcc) {
		t.Errorf("round trip = %v, want %v", back, avcc)
	}
}

func TestExtractSPSPPS_ReturnsParameterSetsWithoutStartCodes(t *testing.T) {
	annexB := bytes.Join([][]byte{
		annexBStartCode4(nalSPS, 0x11, 0x12),
		annexBStartCode4(nalPPS, 0x22),
		annexBStartCode4(nalIDR, 0x99),
	}, nil)

	sps, pps := extractSPSPPS(annexB)
	if !bytes.Equal(sps, []byte{nalSPS, 0x11, 0x12}) {
		t.Errorf("sps = %v, want [67 11 12]", sps)
	}
	if !bytes.Equal(pps, []byte{nalPPS, 0x22}) {
		t.Errorf("pps = %v, want [68 22]", pps)
	}
}

func TestExtractSPSPPS_LastParameterSetWins(t *testing.T) {
	annexB := bytes.Join([][]byte{
		annexBStartCode4(nalSPS, 0x01),
		annexBStartCode4(nalSPS, 0x02),
	}, nil)
	sps, pps := extractSPSPPS(annexB)
	if !bytes.Equal(sps, []byte{nalSPS, 0x02}) {
		t.Errorf("sps = %v, want last [67 02]", sps)
	}
	if pps != nil {
		t.Errorf("pps = %v, want nil", pps)
	}
}

func TestExtractSPSPPS_NoneFound(t *testing.T) {
	annexB := annexBStartCode4(nalIDR, 0x01)
	sps, pps := extractSPSPPS(annexB)
	if sps != nil || pps != nil {
		t.Errorf("got sps=%v pps=%v, want both nil", sps, pps)
	}
}

func TestReorderAnnexBWithSPSPPS_PrependsCanonicalAndStripsInStream(t *testing.T) {
	// Recording places SPS/PPS at the END of the GOP; the slice comes first.
	inStream := bytes.Join([][]byte{
		annexBStartCode4(nalIDR, 0xAA),
		annexBStartCode4(nalSPS, 0xFF), // stale, must be stripped
		annexBStartCode4(nalPPS, 0xEE), // stale, must be stripped
	}, nil)

	canonicalSPS := []byte{nalSPS, 0x11}
	canonicalPPS := []byte{nalPPS, 0x22}

	got := reorderAnnexBWithSPSPPS(inStream, canonicalSPS, canonicalPPS)

	want := bytes.Join([][]byte{
		annexBStartCode4(canonicalSPS...),
		annexBStartCode4(canonicalPPS...),
		annexBStartCode4(nalIDR, 0xAA),
	}, nil)
	if !bytes.Equal(got, want) {
		t.Errorf("reorder = %v, want %v", got, want)
	}

	// The reordered stream must split into exactly SPS, PPS, IDR in that order.
	nals := splitAnnexB(got)
	if len(nals) != 3 {
		t.Fatalf("got %d NALs after reorder, want 3", len(nals))
	}
	types := []byte{nals[0][0] & 0x1f, nals[1][0] & 0x1f, nals[2][0] & 0x1f}
	if !bytes.Equal(types, []byte{7, 8, 5}) {
		t.Errorf("NAL type order = %v, want [7 8 5]", types)
	}
}
