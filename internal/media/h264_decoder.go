package media

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"

	openh264 "github.com/y9o/go-openh264"
)

var (
	openh264StateMu       sync.Mutex
	openh264Attempt       bool
	openh264Loaded        bool
	openh264LoadErr       error
	openh264Source        string
	openh264Path          string
	openh264LoadedVersion string

	openh264LoadLibrary  = openh264.Open
	openh264CloseLibrary = openh264.Close
	openh264CodecVersion = func() string {
		ver := openh264.WelsGetCodecVersion()
		return fmt.Sprintf("%d.%d.%d", ver.UMajor, ver.UMinor, ver.URevision)
	}
	openh264LibPathsFn = openH264LibPaths

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
func tryLoadOpenH264(path, source string) error {
	if err := openh264LoadLibrary(path); err != nil {
		return err
	}

	version := openh264CodecVersion()
	slog.Info("OpenH264 loaded", "path", path, "source", source, "version", version)
	slog.Info("OpenH264 Video Codec provided by Cisco Systems, Inc.")

	openh264Loaded = true
	openh264LoadErr = nil
	openh264Source = source
	openh264Path = path
	openh264LoadedVersion = version
	return nil
}

// ensureOpenH264 tries to load the OpenH264 library once.
// Search order: OPENH264_LIB env → system paths → verified Vedetta install.
func ensureOpenH264() bool {
	openh264StateMu.Lock()
	defer openh264StateMu.Unlock()

	if openh264Attempt {
		return openh264Loaded
	}
	openh264Attempt = true

	var attempts []string
	recordFailure := func(label string, err error) {
		if err == nil {
			return
		}
		attempts = append(attempts, fmt.Sprintf("%s: %v", label, err))
	}

	if envPath := strings.TrimSpace(os.Getenv("OPENH264_LIB")); envPath != "" {
		if err := tryLoadOpenH264(envPath, "environment"); err == nil {
			return true
		} else {
			recordFailure("OPENH264_LIB", fmt.Errorf("failed to load %q: %w", envPath, err))
		}
	}

	for _, path := range openh264LibPathsFn() {
		if err := tryLoadOpenH264(path, "system"); err == nil {
			return true
		} else {
			recordFailure(path, err)
		}
	}

	if installedPath, installed, err := verifiedInstalledOpenH264Path(); err != nil {
		recordFailure("installed cache", err)
	} else if installed {
		if err := tryLoadOpenH264(installedPath, "installed"); err == nil {
			return true
		} else {
			recordFailure("installed cache", fmt.Errorf("failed to load %q: %w", installedPath, err))
		}
	}

	baseErr := "OpenH264 shared library not found; set OPENH264_LIB, install libopenh264 via the system package manager, or install it from the setup/system page"
	if len(attempts) > 0 {
		openh264LoadErr = fmt.Errorf("%s (attempts: %s)", baseErr, strings.Join(attempts, "; "))
	} else {
		openh264LoadErr = errors.New(baseErr)
	}
	slog.Warn("H264 decode unavailable — detection disabled", "error", openh264LoadErr)
	return openh264Loaded
}

func openH264StateSnapshot() (loaded bool, source, path, version string, loadErr error) {
	openh264StateMu.Lock()
	defer openh264StateMu.Unlock()
	return openh264Loaded, openh264Source, openh264Path, openh264LoadedVersion, openh264LoadErr
}

func resetOpenH264State() {
	openh264StateMu.Lock()
	defer openh264StateMu.Unlock()

	_ = openh264CloseLibrary()
	openh264Attempt = false
	openh264Loaded = false
	openh264LoadErr = nil
	openh264Source = ""
	openh264Path = ""
	openh264LoadedVersion = ""
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
