//go:build darwin && cgo

package media

import (
	"testing"
)

func TestVideoToolboxProbe(t *testing.T) {
	if !probeVideoToolbox() {
		t.Skip("VideoToolbox not available")
	}
}

func TestVideoToolboxDecoder_NilInput(t *testing.T) {
	dec := newVideoToolboxDecoder(HWAccelVT, nil, nil)
	if dec == nil {
		t.Skip("VideoToolbox decoder creation failed")
	}
	defer dec.Close()
	// Empty input should not panic
	if frame := dec.Decode(nil); frame != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestVideoToolboxDecoder_InvalidNAL(t *testing.T) {
	dec := newVideoToolboxDecoder(HWAccelVT, nil, nil)
	if dec == nil {
		t.Skip("VideoToolbox decoder creation failed")
	}
	defer dec.Close()
	// Random garbage should not panic
	garbage := []byte{0, 0, 0, 1, 0xFF, 0xAA, 0xBB}
	if frame := dec.Decode(garbage); frame != nil {
		t.Fatal("expected nil for garbage input")
	}
}

func TestVTLazyDecoder_NoPrematureFallback(t *testing.T) {
	// A transient nil (waiting for SPS/PPS, not a hard session failure) must not
	// trigger the software fallback. Premature fallback would defeat hardware
	// decode for every camera that sends parameter sets only in-band.
	d := &vtLazyDecoder{vt: &vtDecoder{}}
	defer d.Close()
	if frame := d.Decode([]byte{0, 0, 0, 1, 0x41, 0x9A, 0xBB}); frame != nil {
		t.Fatal("expected nil for a non-IDR slice with no parameter sets")
	}
	if d.soft != nil {
		t.Fatal("must not fall back to software on a transient nil")
	}
}
