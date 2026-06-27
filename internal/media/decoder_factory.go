package media

import (
	"log/slog"
	"sync/atomic"
)

// defaultHWAccel holds the process-wide decode preference, set once at startup
// via SetHWAccelPreference and read by NewDefaultFrameDecoder. It is an
// atomic.Value so the startup write and the per-camera reads (cameras start on
// their own goroutines) are race-free. Defaults to auto until set.
var defaultHWAccel atomic.Value // stores HWAccel

// SetHWAccelPreference records the process-wide hardware-decode preference.
// Call once at startup, before any camera starts. Consumers that build decoders
// via NewDefaultFrameDecoder honor this value.
func SetHWAccelPreference(pref HWAccel) {
	defaultHWAccel.Store(pref)
}

// hwAccelPreference returns the configured preference, or HWAccelAuto if unset.
func hwAccelPreference() HWAccel {
	if v, ok := defaultHWAccel.Load().(HWAccel); ok {
		return v
	}
	return HWAccelAuto
}

// NewDefaultFrameDecoder creates a FrameDecoder using the process-wide
// preference set by SetHWAccelPreference (HWAccelAuto when unset). The stream's
// SPS/PPS (from the RTSP track, may be nil) let a hardware backend validate that
// it can initialize for this stream so the factory can fall back to software
// when it cannot.
func NewDefaultFrameDecoder(sps, pps []byte) FrameDecoder {
	return NewFrameDecoder(hwAccelPreference(), sps, pps)
}

// NewFrameDecoder creates a FrameDecoder using the requested preference:
//
//   - auto (default) and software: the bundled OpenH264 software decoder.
//     Hardware decode measured no benefit for vedetta's small detection
//     sub-streams (decode is not a CPU bottleneck there and the GPU readback
//     costs more than it saves), so it is not used by default.
//   - an explicit hardware backend (videotoolbox/vaapi/nvdec): that backend
//     only, with no software fallback. If it cannot initialize, decode is
//     disabled. Honored exactly so an operator can opt in for workloads where it
//     helps (many cameras, or full-resolution decode).
//
// sps/pps may be nil, in which case a hardware backend defers session creation
// to the first frame.
func NewFrameDecoder(pref HWAccel, sps, pps []byte) FrameDecoder {
	switch pref {
	case HWAccelAuto, HWAccelSoftware:
		return newSoftwareDecoder()
	default:
		// Explicit hardware backend: no software fallback. probeAndCreate
		// returns a nil interface when the backend is absent or fails to init.
		return probeAndCreate(pref, sps, pps)
	}
}

// newSoftwareDecoder returns the OpenH264 software decoder, guarding the
// typed-nil-in-interface trap: NewH264Decoder returns a concrete *H264Decoder,
// so a nil result must be returned as an untyped nil interface.
func newSoftwareDecoder() FrameDecoder {
	dec := NewH264Decoder()
	if dec == nil {
		return nil
	}
	return dec
}

// probeAndCreate creates the requested explicit hardware decoder, or nil when
// that backend is not available on this system or cannot initialize.
func probeAndCreate(pref HWAccel, sps, pps []byte) FrameDecoder {
	for _, hw := range platformProbeHW() {
		if hw == pref {
			if dec := platformCreateHW(pref, sps, pps); dec != nil {
				slog.Info("using hardware decoder", "backend", string(pref))
				return dec
			}
		}
	}
	return nil
}

// ProbeHardwareDecoders returns the list of available hardware decoders on this system.
func ProbeHardwareDecoders() []HWAccel {
	return platformProbeHW()
}
