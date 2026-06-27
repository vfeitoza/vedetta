package media

import (
	"image"
	"testing"
)

// mockDecoder is a test double that implements FrameDecoder.
type mockDecoder struct {
	frames []*image.YCbCr
	idx    int
	closed bool
}

func (m *mockDecoder) Decode(nalData []byte) *image.YCbCr {
	if m.idx >= len(m.frames) {
		return nil
	}
	f := m.frames[m.idx]
	m.idx++
	return f
}

func (m *mockDecoder) Flush() *image.YCbCr {
	return nil
}

func (m *mockDecoder) Close() {
	m.closed = true
}

func testYCbCr(w, h int) *image.YCbCr {
	return &image.YCbCr{
		Y:              make([]byte, w*h),
		Cb:             make([]byte, (w/2)*(h/2)),
		Cr:             make([]byte, (w/2)*(h/2)),
		YStride:        w,
		CStride:        w / 2,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, w, h),
	}
}

func TestFrameDecoderInterface(t *testing.T) {
	// Verify *H264Decoder satisfies FrameDecoder when non-nil
	var _ FrameDecoder = (*H264Decoder)(nil)
}

func TestMockDecoderSatisfiesInterface(t *testing.T) {
	var dec FrameDecoder = &mockDecoder{frames: []*image.YCbCr{testYCbCr(640, 480)}}
	frame := dec.Decode([]byte{0, 0, 0, 1, 0x67})
	if frame == nil {
		t.Fatal("expected frame")
	}
	if frame.Rect.Dx() != 640 || frame.Rect.Dy() != 480 {
		t.Fatalf("unexpected dimensions: %v", frame.Rect)
	}
	dec.Close()
}

func TestNewFrameDecoder_Software(t *testing.T) {
	dec := NewFrameDecoder(HWAccelSoftware, nil, nil)
	if dec == nil {
		t.Skip("OpenH264 not available")
	}
	defer dec.Close()
	// Feed empty data should return nil, not panic
	if frame := dec.Decode(nil); frame != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestNewFrameDecoder_Auto(t *testing.T) {
	dec := NewFrameDecoder(HWAccelAuto, nil, nil)
	if dec == nil {
		t.Skip("no decoder available")
	}
	defer dec.Close()
}

func TestNewFrameDecoder_ExplicitBackendNoSoftwareFallback(t *testing.T) {
	// An explicit hardware backend must be honored exactly: when it is not
	// available, NewFrameDecoder returns nil rather than silently downgrading to
	// software. On a host that has VideoToolbox this path returns the hardware
	// decoder, so skip there.
	if len(ProbeHardwareDecoders()) > 0 {
		t.Skip("hardware decoder present; explicit-backend path returns it")
	}
	if dec := NewFrameDecoder(HWAccelVT, nil, nil); dec != nil {
		dec.Close()
		t.Fatal("expected nil: explicit videotoolbox must not fall back to software")
	}
}

func TestProbeHardwareDecoders(t *testing.T) {
	avail := ProbeHardwareDecoders()
	// Should not panic; result depends on platform
	t.Logf("available hardware decoders: %v", avail)
}

func TestParseHWAccel(t *testing.T) {
	cases := []struct {
		in   string
		want HWAccel
		ok   bool
	}{
		{"", HWAccelAuto, true},
		{"auto", HWAccelAuto, true},
		{"AUTO", HWAccelAuto, true},
		{"  software  ", HWAccelSoftware, true},
		{"videotoolbox", HWAccelVT, true},
		{"vaapi", HWAccelAuto, false}, // dropped backend: not ok, degrades to auto
		{"nvdec", HWAccelAuto, false}, // dropped backend
		{"bogus", HWAccelAuto, false},
	}
	for _, c := range cases {
		got, ok := ParseHWAccel(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseHWAccel(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestHWAccelPreferenceRoundTrip(t *testing.T) {
	orig := hwAccelPreference()
	t.Cleanup(func() { SetHWAccelPreference(orig) })

	SetHWAccelPreference(HWAccelSoftware)
	if got := hwAccelPreference(); got != HWAccelSoftware {
		t.Fatalf("hwAccelPreference() = %q, want software", got)
	}

	// NewDefaultFrameDecoder must use the stored preference. With software
	// forced, it yields the OpenH264 decoder when the library is present.
	dec := NewDefaultFrameDecoder(nil, nil)
	if dec == nil {
		t.Skip("OpenH264 not available for software decode")
	}
	dec.Close()
}
