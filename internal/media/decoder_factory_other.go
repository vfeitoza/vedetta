//go:build !darwin && !linux

package media

// On platforms other than macOS and Linux, Vedetta ships no hardware-decode
// backend: it stays a single static binary with no external codec dependency.
// Decode always uses the bundled OpenH264 software decoder. NewFrameDecoder
// falls back to it when probing finds nothing here. (Linux has its own factory
// in decoder_factory_linux.go; macOS in decoder_factory_darwin*.go.)
func platformProbeHW() []HWAccel {
	return nil
}

func platformCreateHW(_ HWAccel, _, _ []byte) FrameDecoder {
	return nil
}
