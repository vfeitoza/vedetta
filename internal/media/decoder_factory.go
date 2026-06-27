package media

import "log/slog"

// NewFrameDecoder creates a FrameDecoder using the preferred acceleration.
// Falls back to OpenH264 software decode if the preferred backend is unavailable.
func NewFrameDecoder(pref HWAccel) FrameDecoder {
	if pref != HWAccelSoftware {
		if dec := probeAndCreate(pref); dec != nil {
			return dec
		}
	}
	// Fallback to software (OpenH264)
	dec := NewH264Decoder()
	if dec == nil {
		return nil
	}
	return dec
}

// probeAndCreate attempts to create a hardware-accelerated decoder.
func probeAndCreate(pref HWAccel) FrameDecoder {
	available := platformProbeHW()
	if len(available) == 0 {
		return nil
	}
	if pref == HWAccelAuto {
		// Try first available
		for _, hw := range available {
			if dec := platformCreateHW(hw); dec != nil {
				slog.Info("using hardware decoder", "backend", string(hw))
				return dec
			}
		}
		return nil
	}
	// Try specific preference
	for _, hw := range available {
		if hw == pref {
			return platformCreateHW(pref)
		}
	}
	return nil
}

// ProbeHardwareDecoders returns the list of available hardware decoders on this system.
func ProbeHardwareDecoders() []HWAccel {
	return platformProbeHW()
}
