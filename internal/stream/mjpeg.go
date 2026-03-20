package stream

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const (
	mjpegBoundary = "watchpostframe"
	mjpegFPS      = 5
)

// SnapshotFunc returns the latest snapshot for a camera.
type SnapshotFunc func() *image.RGBA

// RGB24SnapshotFunc copies the raw RGB24 frame into dst and returns dimensions.
// Returns false if no frame is available.
type RGB24SnapshotFunc func(dst []byte) (w, h int, ok bool)

// MJPEGHandler returns an HTTP handler that serves a multipart MJPEG stream.
// This variant accepts a SnapshotFunc that returns *image.RGBA.
func MJPEGHandler(snapshotFn SnapshotFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMJPEGHeaders(w)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		flusher.Flush()

		ticker := time.NewTicker(time.Second / mjpegFPS)
		defer ticker.Stop()

		var jpegBuf bytes.Buffer
		jpegOpts := &jpeg.Options{Quality: 75}
		headerBuf := make([]byte, 0, 128)

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				img := snapshotFn()
				if img == nil {
					continue
				}

				jpegBuf.Reset()
				if err := jpeg.Encode(&jpegBuf, img, jpegOpts); err != nil {
					slog.Error("MJPEG encode error", "error", err)
					continue
				}

				headerBuf = appendFrameHeader(headerBuf[:0], jpegBuf.Len())
				if _, err := w.Write(headerBuf); err != nil {
					return
				}
				if _, err := w.Write(jpegBuf.Bytes()); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

// MJPEGHandlerRGB24 returns an optimized MJPEG handler that avoids per-frame
// RGBA allocation by working directly with raw RGB24 data.
func MJPEGHandlerRGB24(snapshotFn RGB24SnapshotFunc, frameSize int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMJPEGHeaders(w)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		flusher.Flush()

		ticker := time.NewTicker(time.Second / mjpegFPS)
		defer ticker.Stop()

		rgb24Buf := make([]byte, frameSize)
		var rgbaImg *image.RGBA
		var jpegBuf bytes.Buffer
		jpegOpts := &jpeg.Options{Quality: 75}
		headerBuf := make([]byte, 0, 128)

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fw, fh, ok := snapshotFn(rgb24Buf)
				if !ok {
					continue
				}

				// Reuse the RGBA image if dimensions match
				if rgbaImg == nil || rgbaImg.Rect.Dx() != fw || rgbaImg.Rect.Dy() != fh {
					rgbaImg = image.NewRGBA(image.Rect(0, 0, fw, fh))
				}
				rgb24ToRGBA(rgb24Buf, rgbaImg.Pix, fw*fh)

				jpegBuf.Reset()
				if err := jpeg.Encode(&jpegBuf, rgbaImg, jpegOpts); err != nil {
					slog.Error("MJPEG encode error", "error", err)
					continue
				}

				headerBuf = appendFrameHeader(headerBuf[:0], jpegBuf.Len())
				if _, err := w.Write(headerBuf); err != nil {
					return
				}
				if _, err := w.Write(jpegBuf.Bytes()); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

func writeMJPEGHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", mjpegBoundary))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

// appendFrameHeader appends the MIME multipart frame header to dst and returns it.
func appendFrameHeader(dst []byte, contentLength int) []byte {
	dst = append(dst, "\r\n--"...)
	dst = append(dst, mjpegBoundary...)
	dst = append(dst, "\r\nContent-Type: image/jpeg\r\nContent-Length: "...)
	dst = strconv.AppendInt(dst, int64(contentLength), 10)
	dst = append(dst, "\r\n\r\n"...)
	return dst
}

// rgb24ToRGBA converts packed RGB24 pixels into RGBA pixels in-place.
func rgb24ToRGBA(src, dst []byte, n int) {
	for i := range n {
		si := i * 3
		di := i * 4
		dst[di+0] = src[si+0]
		dst[di+1] = src[si+1]
		dst[di+2] = src[si+2]
		dst[di+3] = 255
	}
}
