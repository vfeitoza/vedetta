package detect

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
)

const (
	scrfdFileName = "det_500m.onnx"
	scrfdURL      = "https://github.com/yakhyo/facial-analysis/releases/download/v0.0.1/" + scrfdFileName

	mobileFaceNetFileName = "w600k_mbf.onnx"
	buffaloZipURL         = "https://github.com/nicetester01ued/insightface-models/releases/download/v0.7/buffalo_sc.zip"
)

// cachedFaceModelPath returns the path where a cached face model should be.
func cachedFaceModelPath(fileName string) string {
	return filepath.Join(modelCacheDir(), fileName)
}

// downloadSCRFD downloads the SCRFD face detection model and caches it locally.
func downloadSCRFD() (string, error) {
	destPath := cachedFaceModelPath(scrfdFileName)
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	if err := os.MkdirAll(modelCacheDir(), 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	slog.Info("downloading SCRFD face detection model", "url", scrfdURL)

	resp, err := http.Get(scrfdURL) //nolint:gosec // known external URL
	if err != nil {
		return "", fmt.Errorf("download SCRFD model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download SCRFD model: HTTP %d", resp.StatusCode)
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
		return "", fmt.Errorf("write SCRFD model: %w", err)
	}

	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename SCRFD model: %w", err)
	}

	slog.Info("SCRFD model downloaded and cached", "path", destPath, "size", n)
	return destPath, nil
}

// downloadMobileFaceNet downloads the buffalo_sc.zip and extracts w600k_mbf.onnx.
func downloadMobileFaceNet() (string, error) {
	destPath := cachedFaceModelPath(mobileFaceNetFileName)
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	if err := os.MkdirAll(modelCacheDir(), 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	slog.Info("downloading MobileFaceNet embedding model", "url", buffaloZipURL)

	resp, err := http.Get(buffaloZipURL) //nolint:gosec // known external URL
	if err != nil {
		return "", fmt.Errorf("download MobileFaceNet: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download MobileFaceNet: HTTP %d", resp.StatusCode)
	}

	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read MobileFaceNet zip: %w", err)
	}

	slog.Info("extracting MobileFaceNet model from zip", "zip_size", len(zipData))

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}

	for _, f := range zr.File {
		if filepath.Base(f.Name) != mobileFaceNetFileName {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open %s in zip: %w", f.Name, err)
		}

		tmp := destPath + ".tmp"
		outFile, err := os.Create(tmp)
		if err != nil {
			rc.Close()
			return "", fmt.Errorf("create temp file: %w", err)
		}

		n, err := io.Copy(outFile, rc)
		rc.Close()
		if closeErr := outFile.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("extract MobileFaceNet: %w", err)
		}

		if err := os.Rename(tmp, destPath); err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("rename MobileFaceNet model: %w", err)
		}

		slog.Info("MobileFaceNet model extracted and cached", "path", destPath, "size", n)
		return destPath, nil
	}

	return "", fmt.Errorf("%s not found in buffalo_sc.zip", mobileFaceNetFileName)
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
		cachedFaceModelPath(defaultFileName),
	}
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			slog.Info("found face model at", "path", path)
			return data, nil
		}
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
