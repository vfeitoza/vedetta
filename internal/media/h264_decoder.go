package media

import (
	"fmt"
	"image"
	"log/slog"
	"os"
	"runtime"
	"sync"

	openh264 "github.com/y9o/go-openh264"
)

var (
	openh264Once    sync.Once
	openh264Loaded  bool
	openh264LoadErr error

	// openh264Mu serializes all OpenH264 C library calls. The purego
	// bindings use dlopen'd function pointers that share global state,
	// making concurrent calls from multiple goroutines unsafe.
	openh264Mu sync.Mutex
)

// openH264LibPaths returns candidate paths for the OpenH264 shared library.
func openH264LibPaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"libopenh264.dylib",
			"/opt/homebrew/lib/libopenh264.dylib",
			"/usr/local/lib/libopenh264.dylib",
		}
	case "linux":
		return []string{
			"libopenh264.so",
			"/usr/lib/libopenh264.so",
			"/usr/lib/x86_64-linux-gnu/libopenh264.so",
			"/usr/lib/aarch64-linux-gnu/libopenh264.so",
			"/usr/local/lib/libopenh264.so",
		}
	default:
		return []string{"libopenh264.so"}
	}
}

// tryLoadOpenH264 attempts to load the library from a given path.
func tryLoadOpenH264(path string) bool {
	if err := openh264.Open(path); err == nil {
		ver := openh264.WelsGetCodecVersion()
		slog.Info("OpenH264 loaded",
			"path", path,
			"version", fmt.Sprintf("%d.%d.%d", ver.UMajor, ver.UMinor, ver.URevision),
		)
		slog.Info("OpenH264 Video Codec provided by Cisco Systems, Inc.")
		openh264Loaded = true
		return true
	}
	return false
}

// ensureOpenH264 tries to load the OpenH264 library once.
// Search order: OPENH264_LIB env → system paths.
func ensureOpenH264() bool {
	openh264Once.Do(func() {
		// 1. Environment variable
		if envPath := os.Getenv("OPENH264_LIB"); envPath != "" {
			if tryLoadOpenH264(envPath) {
				return
			}
		}

		// 2. System paths
		for _, path := range openH264LibPaths() {
			if tryLoadOpenH264(path) {
				return
			}
		}

		openh264LoadErr = fmt.Errorf(
			"OpenH264 shared library not found; set OPENH264_LIB or install libopenh264 via the system package manager",
		)
		slog.Warn("H264 decode unavailable — detection disabled", "error", openh264LoadErr)
	})
	return openh264Loaded
}

// H264Decoder wraps OpenH264 for decoding H264 NAL units to YCbCr images.
type H264Decoder struct {
	decoder *openh264.ISVCDecoder
}

// NewH264Decoder creates a new H264 decoder. Returns nil if OpenH264 isn't available.
func NewH264Decoder() *H264Decoder {
	if !ensureOpenH264() {
		return nil
	}

	openh264Mu.Lock()
	defer openh264Mu.Unlock()

	var dec *openh264.ISVCDecoder
	if ret := openh264.WelsCreateDecoder(&dec); ret != 0 || dec == nil {
		slog.Error("WelsCreateDecoder failed", "ret", ret)
		return nil
	}

	param := openh264.SDecodingParam{}
	param.EEcActiveIdc = openh264.ERROR_CON_SLICE_MV_COPY_CROSS_IDR_FREEZE_RES_CHANGE
	if ret := dec.Initialize(&param); ret != 0 {
		slog.Error("OpenH264 Initialize failed", "ret", ret)
		openh264.WelsDestroyDecoder(dec)
		return nil
	}

	return &H264Decoder{decoder: dec}
}

// Decode decodes H264 NAL units (with start codes) and returns YCbCr 4:2:0 image.
// Returns nil if no frame was produced (need more data).
//
// Separate decoder instances are thread-safe — only create/destroy and
// encoder operations need the global mutex.
func (d *H264Decoder) Decode(nalData []byte) *image.YCbCr {
	if d == nil || d.decoder == nil || len(nalData) == 0 {
		return nil
	}

	var dst [3][]byte
	var bufInfo openh264.SBufferInfo

	ret := d.decoder.DecodeFrameNoDelay(nalData, len(nalData), &dst, &bufInfo)
	if ret != 0 {
		return nil
	}

	if dst[0] == nil {
		return nil
	}

	sysBuf := bufInfo.UsrData_sSystemBuffer()
	w := int(sysBuf.IWidth)
	h := int(sysBuf.IHeight)
	yStride := int(sysBuf.IStride[0])
	cStride := int(sysBuf.IStride[1])

	if w <= 0 || h <= 0 {
		return nil
	}

	// Copy planes — OpenH264 owns the source buffers
	yLen := yStride * h
	cLen := cStride * (h / 2)

	y := make([]byte, yLen)
	cb := make([]byte, cLen)
	cr := make([]byte, cLen)

	copy(y, dst[0][:yLen])
	copy(cb, dst[1][:cLen])
	copy(cr, dst[2][:cLen])

	return &image.YCbCr{
		Y:              y,
		Cb:             cb,
		Cr:             cr,
		YStride:        yStride,
		CStride:        cStride,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, w, h),
	}
}

// Flush retrieves any buffered frame from the decoder without feeding new data.
func (d *H264Decoder) Flush() *image.YCbCr {
	if d == nil || d.decoder == nil {
		return nil
	}

	var dst [3][]byte
	var bufInfo openh264.SBufferInfo

	ret := d.decoder.FlushFrame(&dst, &bufInfo)
	if ret != 0 || dst[0] == nil {
		return nil
	}

	sysBuf := bufInfo.UsrData_sSystemBuffer()
	w := int(sysBuf.IWidth)
	h := int(sysBuf.IHeight)
	yStride := int(sysBuf.IStride[0])
	cStride := int(sysBuf.IStride[1])

	if w <= 0 || h <= 0 {
		return nil
	}

	yLen := yStride * h
	cLen := cStride * (h / 2)

	y := make([]byte, yLen)
	cb := make([]byte, cLen)
	cr := make([]byte, cLen)

	copy(y, dst[0][:yLen])
	copy(cb, dst[1][:cLen])
	copy(cr, dst[2][:cLen])

	return &image.YCbCr{
		Y:              y,
		Cb:             cb,
		Cr:             cr,
		YStride:        yStride,
		CStride:        cStride,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, w, h),
	}
}

// Close releases the decoder resources.
func (d *H264Decoder) Close() {
	if d != nil && d.decoder != nil {
		openh264Mu.Lock()
		defer openh264Mu.Unlock()
		d.decoder.Uninitialize()
		openh264.WelsDestroyDecoder(d.decoder)
		d.decoder = nil
	}
}

// OpenH264Available returns whether the OpenH264 library was loaded.
func OpenH264Available() bool {
	return ensureOpenH264()
}

// OpenH264Lock acquires the global OpenH264 mutex. Callers that use the
// encoder (or any other OpenH264 API not wrapped by H264Decoder) must
// hold this lock for the duration of each C library call.
func OpenH264Lock()   { openh264Mu.Lock() }
func OpenH264Unlock() { openh264Mu.Unlock() }

// ycbcrToRGB24Scaled converts a YCbCr image to RGB24 at the target resolution.
func ycbcrToRGB24Scaled(img *image.YCbCr, targetW, targetH int) []byte {
	srcW := img.Rect.Dx()
	srcH := img.Rect.Dy()
	rgb := make([]byte, targetW*targetH*3)

	for dy := range targetH {
		sy := dy * srcH / targetH
		for dx := range targetW {
			sx := dx * srcW / targetW

			yi := sy*img.YStride + sx
			ci := (sy/2)*img.CStride + (sx / 2)

			yy := int(img.Y[yi])
			cbb := int(img.Cb[ci]) - 128
			crr := int(img.Cr[ci]) - 128

			r := yy + ((91881*crr + 32768) >> 16)
			g := yy - ((22554*cbb + 46802*crr + 32768) >> 16)
			b := yy + ((116130*cbb + 32768) >> 16)

			if r < 0 {
				r = 0
			} else if r > 255 {
				r = 255
			}
			if g < 0 {
				g = 0
			} else if g > 255 {
				g = 255
			}
			if b < 0 {
				b = 0
			} else if b > 255 {
				b = 255
			}

			di := (dy*targetW + dx) * 3
			rgb[di] = byte(r)
			rgb[di+1] = byte(g)
			rgb[di+2] = byte(b)
		}
	}

	return rgb
}
