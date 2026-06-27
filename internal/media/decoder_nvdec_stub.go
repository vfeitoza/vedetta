//go:build linux && (!cgo || !nvdec)

package media

import "errors"

// Stub NVDEC hooks for Linux builds without -tags nvdec (or without cgo). The
// real implementations live in decoder_nvdec.go.
func nvdecAvailable() bool { return false }

func newNVDECBackend() (FrameDecoder, error) {
	return nil, errors.New("nvdec backend not built (rebuild with -tags nvdec)")
}
