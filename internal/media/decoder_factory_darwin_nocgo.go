//go:build darwin && !cgo

package media

func platformProbeHW() []HWAccel {
	return nil
}

func platformCreateHW(_ HWAccel) FrameDecoder {
	return nil
}

func probeVideoToolbox() bool {
	return false
}

func newVideoToolboxDecoder() FrameDecoder {
	return nil
}
