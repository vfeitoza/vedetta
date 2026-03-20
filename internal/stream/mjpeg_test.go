package stream

import (
	"bufio"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeTestImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		}
	}
	return img
}

func TestMJPEGHandlerProducesValidMultipart(t *testing.T) {
	img := makeTestImage()
	handler := MJPEGHandler(func() *image.RGBA { return img })

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to GET MJPEG stream: %v", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "multipart/x-mixed-replace") {
		t.Fatalf("expected multipart content type, got: %s", contentType)
	}
	if !strings.Contains(contentType, mjpegBoundary) {
		t.Fatalf("content type missing boundary, got: %s", contentType)
	}

	scanner := bufio.NewScanner(resp.Body)
	foundJPEGHeader := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Content-Type: image/jpeg") {
			foundJPEGHeader = true
			break
		}
	}
	if !foundJPEGHeader {
		t.Fatal("did not find JPEG content type header in MJPEG stream")
	}
}

func TestMJPEGHandlerNilSnapshot(t *testing.T) {
	handler := MJPEGHandler(func() *image.RGBA { return nil })

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to GET: %v", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "multipart/x-mixed-replace") {
		t.Fatalf("expected multipart content type, got: %s", contentType)
	}
}

func TestMJPEGHandlerRGB24ProducesValidMultipart(t *testing.T) {
	const w, h = 64, 64
	frameSize := w * h * 3
	rgb24 := make([]byte, frameSize)
	for i := 0; i < w*h; i++ {
		rgb24[i*3+0] = 128
		rgb24[i*3+1] = 64
		rgb24[i*3+2] = 32
	}

	snapshotFn := func(dst []byte) (int, int, bool) {
		copy(dst, rgb24)
		return w, h, true
	}

	handler := MJPEGHandlerRGB24(snapshotFn, frameSize)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to GET MJPEG stream: %v", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "multipart/x-mixed-replace") {
		t.Fatalf("expected multipart content type, got: %s", contentType)
	}
	if !strings.Contains(contentType, mjpegBoundary) {
		t.Fatalf("content type missing boundary, got: %s", contentType)
	}

	scanner := bufio.NewScanner(resp.Body)
	foundJPEGHeader := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Content-Type: image/jpeg") {
			foundJPEGHeader = true
			break
		}
	}
	if !foundJPEGHeader {
		t.Fatal("did not find JPEG content type header in RGB24 MJPEG stream")
	}
}

func TestMJPEGHandlerRGB24NoFrame(t *testing.T) {
	snapshotFn := func(dst []byte) (int, int, bool) {
		return 0, 0, false
	}

	handler := MJPEGHandlerRGB24(snapshotFn, 64*64*3)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to GET: %v", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "multipart/x-mixed-replace") {
		t.Fatalf("expected multipart content type, got: %s", contentType)
	}
}

func TestAppendFrameHeader(t *testing.T) {
	header := appendFrameHeader(nil, 12345)
	s := string(header)

	if !strings.Contains(s, "--"+mjpegBoundary) {
		t.Fatalf("header missing boundary: %q", s)
	}
	if !strings.Contains(s, "Content-Type: image/jpeg") {
		t.Fatalf("header missing content type: %q", s)
	}
	if !strings.Contains(s, "Content-Length: 12345") {
		t.Fatalf("header missing content length: %q", s)
	}
}

func TestRGB24ToRGBA(t *testing.T) {
	src := []byte{255, 128, 0, 0, 64, 192}
	dst := make([]byte, 8)
	rgb24ToRGBA(src, dst, 2)

	expected := []byte{255, 128, 0, 255, 0, 64, 192, 255}
	for i, b := range expected {
		if dst[i] != b {
			t.Fatalf("byte %d: got %d, want %d", i, dst[i], b)
		}
	}
}
