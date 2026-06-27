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
	dec := newVideoToolboxDecoder(nil, nil)
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
	dec := newVideoToolboxDecoder(nil, nil)
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

