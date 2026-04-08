package artifact

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newTestClient(fn roundTripFunc) HTTPClient {
	return &http.Client{Transport: fn}
}

func TestDownloadVerifiesChecksumAndSize(t *testing.T) {
	spec := Spec{
		Name:     "model",
		URL:      "https://example.test/model.onnx",
		SHA256:   "8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8",
		MaxBytes: 16,
	}
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != spec.URL {
			t.Fatalf("unexpected URL: %s", req.URL)
		}
		body := []byte("alpha")
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	data, err := Download(context.Background(), client, spec)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if string(data) != "alpha" {
		t.Fatalf("Download() = %q, want %q", string(data), "alpha")
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	spec := Spec{
		Name:     "model",
		URL:      "https://example.test/model.onnx",
		SHA256:   strings.Repeat("0", 64),
		MaxBytes: 16,
	}
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: 5,
			Body:          io.NopCloser(strings.NewReader("alpha")),
		}, nil
	})

	_, err := Download(context.Background(), client, spec)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Download() error = %v, want checksum mismatch", err)
	}
}

func TestDownloadRejectsOversizedContentLength(t *testing.T) {
	spec := Spec{
		Name:     "model",
		URL:      "https://example.test/model.onnx",
		SHA256:   strings.Repeat("0", 64),
		MaxBytes: 4,
	}
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: 10,
			Body:          io.NopCloser(strings.NewReader("alpha")),
		}, nil
	})

	_, err := Download(context.Background(), client, spec)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("Download() error = %v, want size limit error", err)
	}
}

func TestDownloadRejectsOversizedBodyWithoutContentLength(t *testing.T) {
	spec := Spec{
		Name:     "model",
		URL:      "https://example.test/model.onnx",
		SHA256:   strings.Repeat("0", 64),
		MaxBytes: 4,
	}
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: -1,
			Body:          io.NopCloser(strings.NewReader("alphabet")),
		}, nil
	})

	_, err := Download(context.Background(), client, spec)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("Download() error = %v, want size limit error", err)
	}
}
