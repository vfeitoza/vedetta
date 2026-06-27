//go:build linux && cgo && (vaapi || nvdec)

package media

import "image"

// nv12PlanesToYCbCr converts NV12 plane data (Y plane plus interleaved UV) to
// *image.YCbCr. Shared by the VA-API and NVDEC backends, which both download
// frames from the GPU as NV12.
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
