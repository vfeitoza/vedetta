//go:build linux && (!cgo || !hwaccel)

package media

// Default Linux build: no hardware-decode backend is compiled in (that needs
// -tags hwaccel and cgo, which link libavcodec/libva). Decode uses the bundled
// OpenH264 software decoder. NewFrameDecoder falls back to it when probing finds
// nothing here, keeping the default binary a single static file with no external
// codec dependency.
func platformProbeHW() []HWAccel {
	return nil
}

func platformCreateHW(_ HWAccel, _, _ []byte) FrameDecoder {
	return nil
}
