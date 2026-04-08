package detect

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rvben/vedetta/internal/artifact"
)

const (
	modelFileName = "yolov8n.onnx"
	modelURL      = "https://github.com/rvben/vedetta/releases/download/v0.0.1-models/" + modelFileName
	modelSHA256   = "8dafc1bdfdeb0edf001542a26a0d9e395f9ff7aa145df2e28df11d2800ab562b"
)

// modelCacheDir returns the directory used to cache the downloaded model.
func modelCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "vedetta")
	}
	return filepath.Join(os.TempDir(), "vedetta-cache")
}

// cachedModelPath returns the path where the cached model should be.
func cachedModelPath() string {
	return filepath.Join(modelCacheDir(), modelFileName)
}

// downloadModel downloads the YOLO model and caches it locally.
// Returns the path to the cached model file.
func downloadModel() (string, error) {
	cacheDir := modelCacheDir()
	destPath := filepath.Join(cacheDir, modelFileName)
	return ensureCachedArtifact(artifact.Spec{
		Name:     modelFileName,
		URL:      modelURL,
		SHA256:   modelSHA256,
		MaxBytes: maxModelBytes,
	}, destPath)
}

func readVerifiedCachedModel(path string) ([]byte, error) {
	spec := artifact.Spec{
		Name:     modelFileName,
		URL:      modelURL,
		SHA256:   modelSHA256,
		MaxBytes: maxModelBytes,
	}
	if err := artifact.VerifyFile(path, spec); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("cached YOLO model invalid: %w", err)
	}
	return os.ReadFile(path)
}
