package media

import "image"

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

// HWAccel specifies the hardware acceleration preference.
type HWAccel string

const (
	HWAccelAuto     HWAccel = "auto"         // detect best available
	HWAccelVT       HWAccel = "videotoolbox" // macOS VideoToolbox
	HWAccelVAAPI    HWAccel = "vaapi"        // Linux Intel/AMD
	HWAccelNVDEC    HWAccel = "nvdec"        // Linux NVIDIA
	HWAccelSoftware HWAccel = "software"     // OpenH264 only
)
