package media

import "image"

// nv12PlanesToYCbCr converts NV12 plane data (a full Y plane plus an interleaved
// Cb/Cr plane) into an *image.YCbCr with separate chroma planes. The Linux
// VA-API and NVDEC backends download GPU frames as NV12 and use this to hand the
// rest of the pipeline the same *image.YCbCr the software decoder produces.
//
// yStride/uvStride are the source row strides (may exceed the visible width due
// to GPU alignment); the Y plane keeps its stride while the chroma planes are
// repacked tightly to CStride = w/2. It is pure Go (no build tag) so it is unit
// tested in the default suite even though its only non-test callers are the
// build-tag-gated hardware backends.
func nv12PlanesToYCbCr(w, h, yStride, uvStride int, yData, uvData []byte) *image.YCbCr {
	chromaW := w / 2
	chromaH := h / 2

	cb := make([]byte, chromaW*chromaH)
	cr := make([]byte, chromaW*chromaH)

	for row := 0; row < chromaH; row++ {
		srcOff := row * uvStride
		dstOff := row * chromaW
		for col := 0; col < chromaW; col++ {
			cb[dstOff+col] = uvData[srcOff+col*2]
			cr[dstOff+col] = uvData[srcOff+col*2+1]
		}
	}

	return &image.YCbCr{
		Y:              yData[:yStride*h],
		Cb:             cb,
		Cr:             cr,
		YStride:        yStride,
		CStride:        chromaW,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, w, h),
	}
}
