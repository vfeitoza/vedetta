package detect

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/rvben/vedetta/internal/artifact"
)

const (
	maxModelBytes   = 64 << 20
	maxArchiveBytes = 128 << 20
)

func ensureCachedArtifact(spec artifact.Spec, destPath string) (string, error) {
	if err := artifact.VerifyFile(destPath, spec); err == nil {
		return destPath, nil
	} else if err != nil && !os.IsNotExist(err) {
		slog.Warn("cached artifact invalid, re-downloading", "path", destPath, "artifact", spec.Name, "error", err)
		_ = os.Remove(destPath)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	slog.Info("downloading artifact", "artifact", spec.Name, "url", spec.URL)
	data, err := artifact.Download(context.Background(), nil, spec)
	if err != nil {
		return "", err
	}
	if err := artifact.WriteAtomic(destPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", spec.Name, err)
	}
	slog.Info("artifact cached", "artifact", spec.Name, "path", destPath, "size", len(data))
	return destPath, nil
}

func ensureCachedExtractedArtifact(archiveSpec, extractedSpec artifact.Spec, entryName, destPath string) (string, error) {
	if err := artifact.VerifyFile(destPath, extractedSpec); err == nil {
		return destPath, nil
	} else if err != nil && !os.IsNotExist(err) {
		slog.Warn("cached artifact invalid, re-downloading", "path", destPath, "artifact", extractedSpec.Name, "error", err)
		_ = os.Remove(destPath)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	slog.Info("downloading artifact archive", "artifact", archiveSpec.Name, "url", archiveSpec.URL)
	zipData, err := artifact.Download(context.Background(), nil, archiveSpec)
	if err != nil {
		return "", err
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("open %s archive: %w", archiveSpec.Name, err)
	}

	for _, f := range zr.File {
		if filepath.Base(f.Name) != entryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open %s in %s: %w", f.Name, archiveSpec.Name, err)
		}
		limited := io.LimitReader(rc, extractedSpec.MaxBytes+1)
		data, err := io.ReadAll(limited)
		_ = rc.Close()
		if err != nil {
			return "", fmt.Errorf("extract %s: %w", extractedSpec.Name, err)
		}
		if int64(len(data)) > extractedSpec.MaxBytes {
			return "", fmt.Errorf("extract %s: artifact exceeded %d bytes", extractedSpec.Name, extractedSpec.MaxBytes)
		}
		if err := artifact.VerifyBytes(extractedSpec, data); err != nil {
			return "", err
		}
		if err := artifact.WriteAtomic(destPath, data, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", extractedSpec.Name, err)
		}
		slog.Info("artifact extracted and cached", "artifact", extractedSpec.Name, "path", destPath, "size", len(data))
		return destPath, nil
	}

	return "", fmt.Errorf("%s not found in %s", entryName, archiveSpec.Name)
}
