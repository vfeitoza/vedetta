package detect

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
)

const (
	modelFileName = "yolov8n.onnx"
	modelURL      = "https://github.com/rvben/vedetta/releases/download/v0.0.1-models/" + modelFileName
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

	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	slog.Info("downloading YOLO model", "url", modelURL)

	resp, err := http.Get(modelURL) //nolint:gosec // project's own GitHub URL
	if err != nil {
		return "", fmt.Errorf("download model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download model: HTTP %d", resp.StatusCode)
	}

	tmp := destPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	n, err := io.Copy(f, resp.Body)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write model: %w", err)
	}

	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename model file: %w", err)
	}

	slog.Info("YOLO model downloaded and cached", "path", destPath, "size", n)
	return destPath, nil
}
