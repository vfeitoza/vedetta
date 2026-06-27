//go:build linux && (!cgo || !vaapi)

package media

import "errors"

// Stub VA-API hooks for Linux builds without -tags vaapi (or without cgo). The
// real implementations live in decoder_vaapi.go.
func vaapiAvailable() bool { return false }

func newVAAPIBackend() (FrameDecoder, error) {
	return nil, errors.New("vaapi backend not built (rebuild with -tags vaapi)")
}
