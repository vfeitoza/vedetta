//go:build linux

package media

import (
	"image"
	"log/slog"
)

func platformProbeHW() []HWAccel {
	var avail []HWAccel
	if probeVAAPI() {
		slog.Info("hardware decoder available", "backend", "vaapi")
		avail = append(avail, HWAccelVAAPI)
	}
	if probeNVDEC() {
		slog.Info("hardware decoder available", "backend", "nvdec")
		avail = append(avail, HWAccelNVDEC)
	}
	return avail
}

func platformCreateHW(pref HWAccel) FrameDecoder {
	switch pref {
	case HWAccelVAAPI:
		dec, err := newVAAPIDecoder()
		if err != nil {
			slog.Warn("vaapi decoder init failed", "error", err)
			return nil
		}
		return dec
	case HWAccelNVDEC:
		dec, err := newNVDECDecoder()
		if err != nil {
			slog.Warn("nvdec decoder init failed", "error", err)
			return nil
		}
		return dec
	default:
		return nil
	}
}

// nv12PlanesToYCbCr converts NV12 plane data (Y + interleaved UV) to *image.YCbCr.
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
