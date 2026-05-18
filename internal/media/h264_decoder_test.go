package media

import (
	"testing"

	openh264 "github.com/y9o/go-openh264"
)

// bufInfoWith builds an SBufferInfo whose reported system-buffer geometry is
// exactly w/h/yStride/cStride, mirroring what OpenH264 fills in on a decoded
// frame.
func bufInfoWith(w, h, yStride, cStride int32) *openh264.SBufferInfo {
	bi := &openh264.SBufferInfo{}
	sb := bi.UsrData_sSystemBuffer()
	sb.IWidth = w
	sb.IHeight = h
	sb.IStride[0] = yStride
	sb.IStride[1] = cStride
	return bi
}

// A well-formed frame must round-trip: the helper copies the decoder's planes
// into Go-owned buffers and reports the geometry verbatim.
func TestFrameFromDecodedCopiesValidFrame(t *testing.T) {
	const w, h, yStride, cStride = 4, 4, 4, 2
	yLen := yStride * h
	cLen := cStride * (h / 2)

	dst := [3][]byte{make([]byte, yLen), make([]byte, cLen), make([]byte, cLen)}
	for i := range dst[0] {
		dst[0][i] = byte(i + 1)
	}

	img := frameFromDecoded(dst, bufInfoWith(w, h, yStride, cStride))
	if img == nil {
		t.Fatal("frameFromDecoded returned nil for a valid frame")
	}
	if img.Rect.Dx() != w || img.Rect.Dy() != h {
		t.Fatalf("rect = %v, want %dx%d", img.Rect, w, h)
	}
	if img.YStride != yStride || img.CStride != cStride {
		t.Fatalf("strides = %d/%d, want %d/%d", img.YStride, img.CStride, yStride, cStride)
	}
	if len(img.Y) != yLen || img.Y[0] != 1 || img.Y[yLen-1] != byte(yLen) {
		t.Fatalf("Y plane not copied correctly: len=%d first=%d last=%d", len(img.Y), img.Y[0], img.Y[len(img.Y)-1])
	}
	// Must be a Go-owned copy, not an alias of the cgo-backed input.
	dst[0][0] = 0xFF
	if img.Y[0] == 0xFF {
		t.Fatal("Y plane aliases the decoder buffer; mutation leaked into the returned frame")
	}
}

// The corruption-prevention case: OpenH264 can report a geometry whose
// stride*height exceeds the plane buffer it actually handed back (a
// transitional/corrupt frame, e.g. mid-stream resolution change with error
// concealment active). The old code did dst[0][:yStride*h] + copy, reading
// far past the cgo-owned buffer into foreign memory. The helper MUST reject
// the frame instead.
func TestFrameFromDecodedRejectsGeometryLargerThanBuffer(t *testing.T) {
	// Buffers sized for a 4x4 frame, but the decoder reports 64x64.
	dst := [3][]byte{make([]byte, 16), make([]byte, 4), make([]byte, 4)}

	if img := frameFromDecoded(dst, bufInfoWith(64, 64, 64, 32)); img != nil {
		t.Fatalf("expected nil for geometry exceeding buffer length, got %v", img.Rect)
	}
}

// Degenerate or garbage geometry (zero/negative dimensions, stride narrower
// than the frame) must be rejected before it can produce an image.YCbCr that
// out-of-bounds-indexes in a pure-Go consumer.
func TestFrameFromDecodedRejectsDegenerateGeometry(t *testing.T) {
	big := [3][]byte{make([]byte, 1<<20), make([]byte, 1<<20), make([]byte, 1<<20)}

	cases := []struct {
		name                       string
		w, h, yStride, cStride int32
	}{
		{"zero width", 0, 4, 4, 2},
		{"zero height", 4, 0, 4, 2},
		{"negative height", 4, -4, 4, 2},
		{"y stride narrower than width", 8, 4, 4, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if img := frameFromDecoded(big, bufInfoWith(c.w, c.h, c.yStride, c.cStride)); img != nil {
				t.Fatalf("expected nil for %s, got %v", c.name, img.Rect)
			}
		})
	}
}

// A nil plane (OpenH264 produced no frame) is not an error - it just means
// "need more data"; the helper returns nil without touching the slices.
func TestFrameFromDecodedNilPlane(t *testing.T) {
	if img := frameFromDecoded([3][]byte{nil, nil, nil}, bufInfoWith(4, 4, 4, 2)); img != nil {
		t.Fatal("expected nil when no plane was produced")
	}
}
