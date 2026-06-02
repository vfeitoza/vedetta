package stream

import (
	"testing"

	"github.com/pion/rtp"
)

// clearParamSetMarker must never mutate its input: the packet handed to a
// webrtc peer is the single shared fan-out clone, and every other consumer
// (recording, MSE, HLS, RTSP republish) must observe its original Marker bit.
// Mutating it in place corrupts access-unit assembly for all of them.
func TestClearParamSetMarkerDoesNotMutateSharedPacket(t *testing.T) {
	// SPS NAL (type 7) that arrived with Marker=1, as Tapo cameras send.
	in := &rtp.Packet{
		Header:  rtp.Header{Marker: true},
		Payload: []byte{0x67, 0x42, 0x00},
	}

	out := clearParamSetMarker(in)

	if !in.Marker {
		t.Error("input packet Marker was mutated; shared clone must be untouched")
	}
	if out.Marker {
		t.Error("returned packet should have Marker cleared for an SPS NAL")
	}
	if out == in {
		t.Error("expected a copy for the marker-cleared packet, got the same pointer")
	}
}

func TestClearParamSetMarkerPassesThroughNonParamSets(t *testing.T) {
	// Non-IDR slice (type 1) with Marker set: must be returned unchanged.
	in := &rtp.Packet{
		Header:  rtp.Header{Marker: true},
		Payload: []byte{0x41, 0x00},
	}

	out := clearParamSetMarker(in)

	if out != in {
		t.Error("non-parameter-set packet should be returned as-is")
	}
	if !out.Marker {
		t.Error("non-parameter-set packet Marker must be preserved")
	}
}
