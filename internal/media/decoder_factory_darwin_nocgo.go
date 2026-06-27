//go:build darwin && !cgo

package media

func platformProbeHW() []HWAccel {
	return nil
}

func platformCreateHW(_ HWAccel, _, _ []byte) FrameDecoder {
	return nil
}

func probeVideoToolbox() bool {
	return false
}

func newVideoToolboxDecoder(_ HWAccel, _, _ []byte) FrameDecoder {
	return nil
}
