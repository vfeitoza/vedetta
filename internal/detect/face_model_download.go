package detect

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/rvben/vedetta/internal/artifact"
)

const (
	scrfdFileName = "det_500m.onnx"
	scrfdURL      = "https://github.com/yakhyo/facial-analysis/releases/download/v0.0.1/" + scrfdFileName
	scrfdSHA256   = "5e4447f50245bbd7966bd6c0fa52938c61474a04ec7def48753668a9d8b4ea3a"

	mobileFaceNetFileName = "w600k_mbf.onnx"
	buffaloZipURL         = "https://github.com/deepinsight/insightface/releases/download/v0.7/buffalo_sc.zip"
	buffaloZipSHA256      = "57d31b56b6ffa911c8a73cfc1707c73cab76efe7f13b675a05223bf42de47c72"
	mobileFaceNetSHA256   = "9cc6e4a75f0e2bf0b1aed94578f144d15175f357bdc05e815e5c4a02b319eb4f"
)

// cachedFaceModelPath returns the path where a cached face model should be.
func cachedFaceModelPath(fileName string) string {
	return filepath.Join(modelCacheDir(), fileName)
}

func faceModelSpec(fileName string) (artifact.Spec, bool) {
	switch fileName {
	case scrfdFileName:
		return artifact.Spec{
			Name:     scrfdFileName,
			URL:      scrfdURL,
			SHA256:   scrfdSHA256,
			MaxBytes: maxModelBytes,
		}, true
	case mobileFaceNetFileName:
		return artifact.Spec{
			Name:     mobileFaceNetFileName,
			URL:      buffaloZipURL + "#" + mobileFaceNetFileName,
			SHA256:   mobileFaceNetSHA256,
			MaxBytes: maxModelBytes,
		}, true
	case osnetFileName:
		return artifact.Spec{
			Name:     osnetFileName,
			URL:      osnetURL,
			SHA256:   osnetSHA256,
			MaxBytes: maxModelBytes,
		}, true
	default:
		return artifact.Spec{}, false
	}
}

func readVerifiedCachedFaceModel(fileName, path string) ([]byte, error) {
	spec, ok := faceModelSpec(fileName)
	if !ok {
		return os.ReadFile(path)
	}
	if err := artifact.VerifyFile(path, spec); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("cached %s invalid: %w", fileName, err)
	}
	return os.ReadFile(path)
}

// downloadSCRFD downloads the SCRFD face detection model and caches it locally.
func downloadSCRFD() (string, error) {
	destPath := cachedFaceModelPath(scrfdFileName)
	return ensureCachedArtifact(artifact.Spec{
		Name:     scrfdFileName,
		URL:      scrfdURL,
		SHA256:   scrfdSHA256,
		MaxBytes: maxModelBytes,
	}, destPath)
}

// downloadMobileFaceNet downloads the buffalo_sc.zip and extracts w600k_mbf.onnx.
func downloadMobileFaceNet() (string, error) {
	destPath := cachedFaceModelPath(mobileFaceNetFileName)
	return ensureCachedExtractedArtifact(
		artifact.Spec{
			Name:     "buffalo_sc.zip",
			URL:      buffaloZipURL,
			SHA256:   buffaloZipSHA256,
			MaxBytes: maxArchiveBytes,
		},
		artifact.Spec{
			Name:     mobileFaceNetFileName,
			URL:      buffaloZipURL + "#" + mobileFaceNetFileName,
			SHA256:   mobileFaceNetSHA256,
			MaxBytes: maxModelBytes,
		},
		mobileFaceNetFileName,
		destPath,
	)
}

// loadFaceModel resolves model bytes using a fallback chain: config path, local file, cache, auto-download.
func loadFaceModel(configPath, defaultFileName string, downloadFn func() (string, error)) ([]byte, error) {
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err == nil {
			return data, nil
		}
		slog.Warn("configured face model not found, trying cache/download",
			"path", configPath, "error", err)
	}

	candidates := []string{
		defaultFileName,
	}
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			slog.Info("found face model at", "path", path)
			return data, nil
		}
	}
	cachedPath := cachedFaceModelPath(defaultFileName)
	if data, err := readVerifiedCachedFaceModel(defaultFileName, cachedPath); err == nil {
		slog.Info("found verified cached face model at", "path", cachedPath)
		return data, nil
	} else if err != nil && !os.IsNotExist(err) {
		slog.Warn("cached face model failed verification, re-downloading",
			"path", cachedPath, "error", err)
	}

	path, err := downloadFn()
	if err != nil {
		return nil, fmt.Errorf("auto-download face model: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read downloaded face model: %w", err)
	}
	return data, nil
}
