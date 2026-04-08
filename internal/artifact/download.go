package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const DefaultTimeout = 30 * time.Second

// Spec describes a remotely fetched artifact that must be integrity checked.
type Spec struct {
	Name     string
	URL      string
	SHA256   string
	MaxBytes int64
}

// HTTPClient is the subset of http.Client used by the downloader.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

var defaultClient HTTPClient = &http.Client{
	Timeout: DefaultTimeout,
}

// Download fetches an artifact, enforcing size and checksum verification.
func Download(ctx context.Context, client HTTPClient, spec Spec) ([]byte, error) {
	if spec.URL == "" {
		return nil, fmt.Errorf("%s: missing URL", spec.Name)
	}
	if spec.SHA256 == "" {
		return nil, fmt.Errorf("%s: missing SHA-256", spec.Name)
	}
	if spec.MaxBytes <= 0 {
		return nil, fmt.Errorf("%s: invalid max size", spec.Name)
	}
	if client == nil {
		client = defaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", spec.Name, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", spec.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", spec.Name, resp.StatusCode)
	}
	if resp.ContentLength > spec.MaxBytes {
		return nil, fmt.Errorf("download %s: artifact too large (%d > %d bytes)", spec.Name, resp.ContentLength, spec.MaxBytes)
	}

	limited := io.LimitReader(resp.Body, spec.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", spec.Name, err)
	}
	if int64(len(data)) > spec.MaxBytes {
		return nil, fmt.Errorf("download %s: artifact exceeded %d bytes", spec.Name, spec.MaxBytes)
	}
	if err := VerifyBytes(spec, data); err != nil {
		return nil, err
	}
	return data, nil
}

// VerifyFile checks that an on-disk file matches the expected checksum.
func VerifyFile(path string, spec Spec) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return VerifyBytes(spec, data)
}

// VerifyBytes checks that data matches the expected checksum.
func VerifyBytes(spec Spec, data []byte) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != spec.SHA256 {
		return fmt.Errorf("%s checksum mismatch: got %s", spec.Name, got)
	}
	return nil
}

// WriteAtomic writes data to path through a temp file and renames it into place.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
