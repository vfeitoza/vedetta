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
//   - software: the bundled OpenH264 software decoder.
//   - auto: a hardware decoder when one initializes for the stream, otherwise
//     software.
//   - an explicit hardware backend (e.g. videotoolbox): that backend only, with
//     no software fallback. If it cannot initialize, decode is disabled. This is
//     honored exactly so an operator can guarantee hardware decode.
//
// sps/pps may be nil, in which case a hardware backend defers session creation
// to the first frame.
func NewFrameDecoder(pref HWAccel, sps, pps []byte) FrameDecoder {
	switch pref {
	case HWAccelSoftware:
		return newSoftwareDecoder()
	case HWAccelAuto:
		if dec := probeAndCreate(pref, sps, pps); dec != nil {
			return dec
		}
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

// probeAndCreate attempts to create a hardware-accelerated decoder, validating
// it against the stream's SPS/PPS when provided.
func probeAndCreate(pref HWAccel, sps, pps []byte) FrameDecoder {
	available := platformProbeHW()
	if len(available) == 0 {
		return nil
	}
	if pref == HWAccelAuto {
		// Try first available
		for _, hw := range available {
			if dec := platformCreateHW(hw, sps, pps); dec != nil {
				slog.Info("using hardware decoder", "backend", string(hw))
				return dec
			}
		}
		return nil
	}
	// Try specific preference
	for _, hw := range available {
		if hw == pref {
			return platformCreateHW(pref, sps, pps)
		}
	}
	return nil
}

// ProbeHardwareDecoders returns the list of available hardware decoders on this system.
func ProbeHardwareDecoders() []HWAccel {
	return platformProbeHW()
}
