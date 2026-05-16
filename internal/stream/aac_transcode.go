package stream

import (
	"encoding/binary"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/g711"
)

// aacEncoder converts 16-bit mono PCM into AAC-LC access units. The only
// production implementation is libfdk-aac loaded via purego (see
// aac_fdk.go); tests substitute a fake through newAACEncoder.
type aacEncoder interface {
	// Encode consumes PCM samples and returns zero or more complete AAC-LC
	// access units. The encoder buffers internally, so a call may return no
	// frames (input still accumulating) or several.
	Encode(pcm []int16) ([][]byte, error)
	Close()
}

// newAACEncoder builds an AAC-LC encoder for the given sample rate and
// channel count. It is a package variable so tests can inject a fake; in
// production it is the libfdk-aac-backed encoder. An error means no usable
// encoder is available, in which case the caller serves video only.
var newAACEncoder = func(sampleRate, channels int) (aacEncoder, error) {
	return newFDKAACEncoder(sampleRate, channels)
}

// g711IsALaw reports whether an RTSP audio codec name denotes A-law (PCMA)
// as opposed to mu-law (PCMU). Cameras advertise one or the other.
func g711IsALaw(codec string) bool { return codec == "PCMA" }

// decodeG711ToPCM turns one RTP G.711 payload into 16-bit mono PCM samples.
// The mediacommon decoders emit big-endian 16-bit LPCM bytes; we widen them
// back to int16 for the encoder.
func decodeG711ToPCM(payload []byte, aLaw bool) []int16 {
	var raw []byte
	if aLaw {
		var lpcm g711.Alaw
		lpcm.Unmarshal(payload)
		raw = lpcm
	} else {
		var lpcm g711.Mulaw
		lpcm.Unmarshal(payload)
		raw = lpcm
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.BigEndian.Uint16(raw[i*2 : i*2+2]))
	}
	return pcm
}
