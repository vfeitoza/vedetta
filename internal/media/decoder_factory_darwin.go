//go:build darwin && cgo

package media

func probeVideoToolbox() bool {
	// VideoToolbox is always available on macOS 10.8+
	return true
}

func newVideoToolboxDecoder(pref HWAccel, sps, pps []byte) FrameDecoder {
	dec := &vtDecoder{}
	if len(sps) > 0 && len(pps) > 0 {
		// Validate up front: seed the parameter sets and create the session now.
		// If VideoToolbox cannot initialize for this stream, return nil so the
		// factory falls back to software instead of silently decoding nothing.
		dec.sps = append([]byte(nil), sps...)
		dec.pps = append([]byte(nil), pps...)
		if !dec.createSession() {
			return nil
		}
		return dec
	}
	// No parameter sets yet: the session is created lazily on the first Decode
	// once in-band SPS/PPS arrive, so it cannot be validated here. Under auto,
	// wrap it so a later init failure falls back to software; under an explicit
	// videotoolbox preference, honor the hardware-only choice with no fallback.
	if pref == HWAccelAuto {
		return &vtLazyDecoder{vt: dec}
	}
	return dec
}

func platformProbeHW() []HWAccel {
	if probeVideoToolbox() {
		return []HWAccel{HWAccelVT}
	}
	return nil
}

func platformCreateHW(pref HWAccel, sps, pps []byte) FrameDecoder {
	if pref == HWAccelVT || pref == HWAccelAuto {
		return newVideoToolboxDecoder(pref, sps, pps)
	}
	return nil
}
