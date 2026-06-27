//go:build darwin

package media

func probeVideoToolbox() bool {
	// VideoToolbox is always available on macOS 10.8+
	return true
}

func newVideoToolboxDecoder() FrameDecoder {
	dec := &vtDecoder{}
	// Session is created lazily on first Decode when SPS/PPS are available
	return dec
}

func platformProbeHW() []HWAccel {
	if probeVideoToolbox() {
		return []HWAccel{HWAccelVT}
	}
	return nil
}

func platformCreateHW(pref HWAccel) FrameDecoder {
	if pref == HWAccelVT || pref == HWAccelAuto {
		return newVideoToolboxDecoder()
	}
	return nil
}
