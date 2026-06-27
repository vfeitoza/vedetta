//go:build linux && cgo && hwaccel

package media

import "log/slog"

// Linux hardware decode (VA-API for Intel/AMD, NVDEC for NVIDIA) is opt-in at
// build time via -tags hwaccel, because the backends link libavcodec/libva. The
// default build excludes this file (see decoder_factory_linux_stub.go) and falls
// back to the bundled OpenH264 software decoder.

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

// platformCreateHW builds the requested Linux hardware decoder. The codec layer
// reads SPS/PPS from the in-band stream, so the parameter sets are unused here.
func platformCreateHW(pref HWAccel, _, _ []byte) FrameDecoder {
	var (
		dec FrameDecoder
		err error
	)
	switch pref {
	case HWAccelVAAPI:
		dec, err = newVAAPIDecoder()
	case HWAccelNVDEC:
		dec, err = newNVDECDecoder()
	default:
		return nil
	}
	if err != nil {
		slog.Warn("hardware decoder init failed", "backend", string(pref), "error", err)
		return nil
	}
	return dec
}
