package detect

import (
	"github.com/rvben/vedetta/internal/artifact"
)

const (
	osnetFileName = "osnet_x0_25.onnx"
	osnetURL      = "https://github.com/rvben/vedetta/releases/download/v0.0.1-models/" + osnetFileName
	osnetSHA256   = "871732ac7f75d951ab0ed1cc3ecdf5ecc9d83eaea58a88117c5dc1d1b5105e5b"
)

func downloadOSNet() (string, error) {
	destPath := cachedFaceModelPath(osnetFileName) // reuse the same cache dir
	return ensureCachedArtifact(artifact.Spec{
		Name:     osnetFileName,
		URL:      osnetURL,
		SHA256:   osnetSHA256,
		MaxBytes: maxModelBytes,
	}, destPath)
}
