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
	HWAccelVAAPI    HWAccel = "vaapi"        // Linux Intel/AMD (opt-in -tags hwaccel)
	HWAccelNVDEC    HWAccel = "nvdec"        // Linux NVIDIA (opt-in -tags hwaccel)
	HWAccelSoftware HWAccel = "software"     // bundled OpenH264 software decode
)

// ParseHWAccel normalizes a config string into a HWAccel preference. An empty
// string maps to HWAccelAuto. Unknown values also map to HWAccelAuto but report
// ok=false, so callers can warn while still degrading gracefully; config
// validation rejects unknown values before this is reached in production.
//
// vaapi and nvdec are accepted on every build, but their backends are only
// compiled into binaries built with the matching build tag. On a binary without
// that backend the factory finds no hardware decoder and the choice resolves to
// no decoder (explicit backend, no software fallback).
func ParseHWAccel(s string) (pref HWAccel, ok bool) {
	switch HWAccel(strings.ToLower(strings.TrimSpace(s))) {
	case "", HWAccelAuto:
		return HWAccelAuto, true
	case HWAccelVT:
		return HWAccelVT, true
	case HWAccelVAAPI:
		return HWAccelVAAPI, true
	case HWAccelNVDEC:
		return HWAccelNVDEC, true
	case HWAccelSoftware:
		return HWAccelSoftware, true
	default:
		return HWAccelAuto, false
	}
}
