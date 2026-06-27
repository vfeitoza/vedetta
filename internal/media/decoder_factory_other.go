//go:build !darwin

package media

// On non-macOS platforms Vedetta ships no hardware-decode backend: it stays a
// single static binary with no external codec dependency (no ffmpeg/libva/CUDA).
// Decode always uses the bundled OpenH264 software decoder. NewFrameDecoder
// falls back to it when probing finds nothing here.
func platformProbeHW() []HWAccel {
	return nil
}

func platformCreateHW(_ HWAccel, _, _ []byte) FrameDecoder {
	return nil
}
