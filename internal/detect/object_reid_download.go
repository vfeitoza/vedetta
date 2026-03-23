package detect

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
)

const (
	osnetFileName = "osnet_x0_25.onnx"
	osnetURL      = "https://github.com/rvben/vedetta/releases/download/v0.0.1-models/" + osnetFileName
)

func downloadOSNet() (string, error) {
	destPath := cachedFaceModelPath(osnetFileName) // reuse the same cache dir
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	if err := os.MkdirAll(modelCacheDir(), 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	slog.Info("downloading OSNet re-ID model", "url", osnetURL)

	resp, err := http.Get(osnetURL) //nolint:gosec // known external URL
	if err != nil {
		return "", fmt.Errorf("download OSNet model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download OSNet model: HTTP %d", resp.StatusCode)
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
		return "", fmt.Errorf("write OSNet model: %w", err)
	}

	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename OSNet model: %w", err)
	}

	slog.Info("OSNet model downloaded and cached", "path", destPath, "size", n)
	return destPath, nil
}
