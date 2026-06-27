//go:build !darwin && !linux

package media

func platformProbeHW() []HWAccel {
	return nil
}

func platformCreateHW(_ HWAccel) FrameDecoder {
	return nil
}
