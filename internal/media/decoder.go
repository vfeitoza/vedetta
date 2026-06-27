package media

import (
	"image"
	"strings"
)

// FrameDecoder decodes H.264 NAL streams to YCbCr frames.
// Implementations must be safe for use from a single goroutine.
type FrameDecoder interface {
	// Decode decodes NAL data (Annex B with start codes) and returns a frame or nil.
	Decode(nalData []byte) *image.YCbCr
	// Flush retrieves any buffered frame without feeding new data.
	Flush() *image.YCbCr
	// Close releases decoder resources.
	Close()
}

// HWAccel specifies the hardware-decode preference.
type HWAccel string

const (
	HWAccelAuto     HWAccel = "auto"         // hardware when available, else software
	HWAccelVT       HWAccel = "videotoolbox" // macOS VideoToolbox
	HWAccelSoftware HWAccel = "software"     // bundled OpenH264 software decode
)

// ParseHWAccel normalizes a config string into a HWAccel preference. An empty
// string maps to HWAccelAuto. Unknown values also map to HWAccelAuto but report
// ok=false, so callers can warn while still degrading gracefully; config
// validation rejects unknown values before this is reached in production.
func ParseHWAccel(s string) (pref HWAccel, ok bool) {
	switch HWAccel(strings.ToLower(strings.TrimSpace(s))) {
	case "", HWAccelAuto:
		return HWAccelAuto, true
	case HWAccelVT:
		return HWAccelVT, true
	case HWAccelSoftware:
		return HWAccelSoftware, true
	default:
		return HWAccelAuto, false
	}
}
