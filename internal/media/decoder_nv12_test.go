package media

import (
	"image"
	"testing"
)

// TestNV12PlanesToYCbCr verifies the NV12 -> YCbCr conversion used by the Linux
// hardware backends: the Y plane passes through with its stride, and the
// interleaved Cb/Cr plane is split correctly (Cb from even bytes, Cr from odd).
// A swapped or off-by-one deinterleave would change the asserted values.
func TestNV12PlanesToYCbCr(t *testing.T) {
	const (
		w, h           = 4, 2
		yStride        = 6 // padded wider than w to exercise stride handling
		uvStride       = 6 // chromaW*2 = 4, padded to 6
		chromaW, chromaH = w / 2, h / 2
	)

	// Y plane: distinct value per pixel (row*16 + col), laid out at yStride.
	yData := make([]byte, yStride*h)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			yData[row*yStride+col] = byte(row*16 + col)
		}
	}

	// Interleaved UV: Cb = 100+index, Cr = 200+index, per chroma sample.
	uvData := make([]byte, uvStride*chromaH)
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			idx := row*chromaW + col
			uvData[row*uvStride+col*2] = byte(100 + idx)
			uvData[row*uvStride+col*2+1] = byte(200 + idx)
		}
	}

	img := nv12PlanesToYCbCr(w, h, yStride, uvStride, yData, uvData)

	if img.Rect != image.Rect(0, 0, w, h) {
		t.Fatalf("Rect = %v, want %v", img.Rect, image.Rect(0, 0, w, h))
	}
	if img.YStride != yStride || img.CStride != chromaW {
		t.Fatalf("strides: Y=%d C=%d, want Y=%d C=%d", img.YStride, img.CStride, yStride, chromaW)
	}
	if img.SubsampleRatio != image.YCbCrSubsampleRatio420 {
		t.Fatalf("subsample ratio = %v, want 420", img.SubsampleRatio)
	}

	// Y values, read back via the stride, must match what we wrote.
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			if got := img.Y[row*img.YStride+col]; got != byte(row*16+col) {
				t.Fatalf("Y[%d,%d] = %d, want %d", col, row, got, row*16+col)
			}
		}
	}

	// Cb/Cr must be tightly packed (CStride = chromaW) and correctly split.
	if len(img.Cb) != chromaW*chromaH || len(img.Cr) != chromaW*chromaH {
		t.Fatalf("chroma plane sizes Cb=%d Cr=%d, want %d", len(img.Cb), len(img.Cr), chromaW*chromaH)
	}
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			idx := row*chromaW + col
			if got := img.Cb[idx]; got != byte(100+idx) {
				t.Fatalf("Cb[%d] = %d, want %d", idx, got, 100+idx)
			}
			if got := img.Cr[idx]; got != byte(200+idx) {
				t.Fatalf("Cr[%d] = %d, want %d", idx, got, 200+idx)
			}
		}
	}
}
