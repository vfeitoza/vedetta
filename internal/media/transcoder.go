package media

import (
	"fmt"
	"image"
	"os"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
)

// scaleYCbCr scales a YCbCr I420 image to fit within (targetW, targetH) while
// preserving aspect ratio. Output dimensions are always even (required by H264).
// Uses nearest-neighbour sampling — sufficient for downscaling security footage.
func scaleYCbCr(src *image.YCbCr, targetW, targetH int) *image.YCbCr {
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()

	// Compute scale to fit within target box, preserve aspect ratio
	scaleW := float64(targetW) / float64(srcW)
	scaleH := float64(targetH) / float64(srcH)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}

	outW := int(float64(srcW)*scale/2) * 2 // round down to even
	outH := int(float64(srcH)*scale/2) * 2

	if outW <= 0 {
		outW = 2
	}
	if outH <= 0 {
		outH = 2
	}

	dst := image.NewYCbCr(image.Rect(0, 0, outW, outH), image.YCbCrSubsampleRatio420)

	for dy := range outH {
		sy := dy * srcH / outH
		for dx := range outW {
			sx := dx * srcW / outW
			dst.Y[dy*dst.YStride+dx] = src.Y[sy*src.YStride+sx]
		}
	}
	// Chroma planes (half resolution for I420)
	for dy := range outH / 2 {
		sy := dy * (srcH / 2) / (outH / 2)
		for dx := range outW / 2 {
			sx := dx * (srcW / 2) / (outW / 2)
			dst.Cb[dy*dst.CStride+dx] = src.Cb[sy*src.CStride+sx]
			dst.Cr[dy*dst.CStride+dx] = src.Cr[sy*src.CStride+sx]
		}
	}

	return dst
}

// shouldTranscode reports whether transcoding from (srcW, srcH) to (targetW, targetH)
// is worth doing. Returns (skip=true, 0, 0) when the source is already at or below
// the target size, or when the area reduction is less than 25%.
// When skip=false, returns the actual output dimensions (aspect-ratio-corrected, even).
func shouldTranscode(srcW, srcH, targetW, targetH int) (skip bool, outW, outH int) {
	// Already at or below target in both dimensions
	if srcW <= targetW && srcH <= targetH {
		return true, 0, 0
	}

	// Compute output dimensions preserving aspect ratio
	scaleW := float64(targetW) / float64(srcW)
	scaleH := float64(targetH) / float64(srcH)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}
	outW = int(float64(srcW)*scale/2) * 2
	outH = int(float64(srcH)*scale/2) * 2
	if outW <= 0 {
		outW = 2
	}
	if outH <= 0 {
		outH = 2
	}

	// Skip if area reduction is less than 25%
	srcArea := srcW * srcH
	outArea := outW * outH
	if float64(outArea) >= float64(srcArea)*0.75 {
		return true, 0, 0
	}

	return false, outW, outH
}

// readSourceResolution reads the H264 video resolution from an fMP4 file
// by parsing the init segment and decoding the SPS from the H264 codec config.
func readSourceResolution(path string) (width, height int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var init fmp4.Init
	if err := init.Unmarshal(f); err != nil {
		return 0, 0, fmt.Errorf("unmarshal init: %w", err)
	}

	for _, track := range init.Tracks {
		h264Codec, ok := track.Codec.(*codecs.H264)
		if !ok {
			continue
		}
		var sps h264.SPS
		if err := sps.Unmarshal(h264Codec.SPS); err != nil {
			return 0, 0, fmt.Errorf("parse SPS: %w", err)
		}
		return sps.Width(), sps.Height(), nil
	}

	return 0, 0, fmt.Errorf("no H264 video track found in init segment")
}
