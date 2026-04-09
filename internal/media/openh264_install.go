package media

import (
	"bytes"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/rvben/vedetta/internal/artifact"
)

const (
	openh264Version         = "2.6.0"
	openh264MaxArchiveBytes = 16 << 20
	openh264MaxLibraryBytes = 32 << 20
)

var (
	openH264InstallRunning atomic.Bool
	openH264Download       = func(ctx context.Context, spec artifact.Spec) ([]byte, error) {
		return artifact.Download(ctx, nil, spec)
	}
	openH264CacheRoot       = defaultOpenH264CacheRoot
	openH264Platform        = func() string { return runtime.GOOS + "/" + runtime.GOARCH }
	openH264InstallSpecFunc = openH264InstallSpecForPlatform
	openH264ExtractLibrary  = extractOpenH264Library
)

var ErrOpenH264InstallInProgress = errors.New("OpenH264 installation already running")

type openH264InstallSpec struct {
	url         string
	filename    string
	archiveSHA  string
	librarySHA  string
	libraryName string
}

type OpenH264Status struct {
	Supported  bool   `json:"supported"`
	Available  bool   `json:"available"`
	Installed  bool   `json:"installed"`
	Installing bool   `json:"installing"`
	Version    string `json:"version,omitempty"`
	Source     string `json:"source,omitempty"`
	Path       string `json:"path,omitempty"`
	Error      string `json:"error,omitempty"`
}

func defaultOpenH264CacheRoot() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "vedetta", "openh264", openh264Version)
	}
	return filepath.Join(os.TempDir(), "vedetta-cache", "openh264", openh264Version)
}

func openH264InstallSpecForPlatform(platform string) (openH264InstallSpec, bool) {
	switch platform {
	case "linux/amd64":
		return openH264InstallSpec{
			url:         "https://ciscobinary.openh264.org/libopenh264-2.6.0-linux64.8.so.bz2",
			filename:    "libopenh264.so.bz2",
			archiveSHA:  "27ab53323c110b76214c1c72222f459d17febbcd1e252136cadc292b0308d75b",
			librarySHA:  "2f0cde7c6a6abcf5cae76942894ea42897fa677bce4ed6c91a24dd1b041d5f04",
			libraryName: "libopenh264.so",
		}, true
	case "linux/arm64":
		return openH264InstallSpec{
			url:         "https://ciscobinary.openh264.org/libopenh264-2.6.0-linux-arm64.8.so.bz2",
			filename:    "libopenh264.so.bz2",
			archiveSHA:  "a78aea7970150f46bcd3bb7994c9e6dd90bd7a9ea785920f5a73f6964e3fcda7",
			librarySHA:  "12e7b33623667cdab0e575170c147b1b36eadb77d0d2aa7ceb5afd3e58902140",
			libraryName: "libopenh264.so",
		}, true
	case "darwin/amd64":
		return openH264InstallSpec{
			url:         "https://ciscobinary.openh264.org/libopenh264-2.6.0-mac-x64.dylib.bz2",
			filename:    "libopenh264.dylib.bz2",
			archiveSHA:  "38b2ed6d1d45b6a3e408c734173f2d67ab44a10d0e154ff3489b89877cd60e7e",
			librarySHA:  "e3dc8bc01fe69363f61fd3c02fd27798537a585eadd38cd808f303d1ee505a19",
			libraryName: "libopenh264.dylib",
		}, true
	case "darwin/arm64":
		return openH264InstallSpec{
			url:         "https://ciscobinary.openh264.org/libopenh264-2.6.0-mac-arm64.dylib.bz2",
			filename:    "libopenh264.dylib.bz2",
			archiveSHA:  "6db362ee5abdab572311aeadb96d3f44b0617d9a4a4b9f4db4cb5ac4d968da71",
			librarySHA:  "052e98bfcf7a9167d22f3bbb3f5988ef79065591f36af8b52924b22b13624551",
			libraryName: "libopenh264.dylib",
		}, true
	default:
		return openH264InstallSpec{}, false
	}
}

func verifiedInstalledOpenH264Path() (string, bool, error) {
	spec, ok := openH264InstallSpecFunc(openH264Platform())
	if !ok {
		return "", false, nil
	}
	path := filepath.Join(openH264CacheRoot(), spec.libraryName)
	verifySpec := artifact.Spec{
		Name:     spec.libraryName,
		URL:      spec.url + "#" + spec.libraryName,
		SHA256:   spec.librarySHA,
		MaxBytes: openh264MaxLibraryBytes,
	}
	if err := artifact.VerifyFile(path, verifySpec); err != nil {
		if os.IsNotExist(err) {
			return path, false, nil
		}
		_ = os.Remove(path)
		return path, false, fmt.Errorf("installed OpenH264 failed verification: %w", err)
	}
	return path, true, nil
}

func OpenH264StatusInfo() OpenH264Status {
	available := ensureOpenH264()
	loaded, source, path, version, loadErr := openH264StateSnapshot()

	installedPath, installed, installErr := verifiedInstalledOpenH264Path()
	status := OpenH264Status{
		Supported:  openH264InstallSupported(),
		Available:  available && loaded,
		Installed:  installed,
		Installing: openH264InstallRunning.Load(),
		Version:    version,
		Source:     source,
		Path:       path,
	}

	if status.Path == "" && installed {
		status.Path = installedPath
	}
	if status.Version == "" && installed {
		status.Version = openh264Version
	}

	switch {
	case installErr != nil:
		status.Error = installErr.Error()
	case shouldExposeOpenH264LoadError(status, loadErr):
		status.Error = loadErr.Error()
	case !status.Supported && !status.Available:
		status.Error = fmt.Sprintf("OpenH264 installer is not available for %s", openH264Platform())
	}
	return status
}

func shouldExposeOpenH264LoadError(status OpenH264Status, loadErr error) bool {
	if loadErr == nil {
		return false
	}
	if status.Installed {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENH264_LIB")) != "" {
		return true
	}
	return strings.Contains(loadErr.Error(), "installed cache")
}

func InstallOpenH264(ctx context.Context) (OpenH264Status, error) {
	current := OpenH264StatusInfo()
	if current.Available {
		return current, nil
	}
	if !openH264InstallRunning.CompareAndSwap(false, true) {
		status := OpenH264StatusInfo()
		status.Installing = true
		return status, ErrOpenH264InstallInProgress
	}
	defer openH264InstallRunning.Store(false)

	spec, ok := openH264InstallSpecFunc(openH264Platform())
	if !ok {
		status := OpenH264StatusInfo()
		return status, fmt.Errorf("OpenH264 installer is not available for %s", openH264Platform())
	}

	archiveSpec := artifact.Spec{
		Name:     spec.filename,
		URL:      spec.url,
		SHA256:   spec.archiveSHA,
		MaxBytes: openh264MaxArchiveBytes,
	}
	archiveData, err := openH264Download(ctx, archiveSpec)
	if err != nil {
		return OpenH264StatusInfo(), fmt.Errorf("download OpenH264 archive: %w", err)
	}

	libraryData, err := openH264ExtractLibrary(archiveData)
	if err != nil {
		return OpenH264StatusInfo(), fmt.Errorf("extract OpenH264 library: %w", err)
	}

	librarySpec := artifact.Spec{
		Name:     spec.libraryName,
		URL:      spec.url + "#" + spec.libraryName,
		SHA256:   spec.librarySHA,
		MaxBytes: openh264MaxLibraryBytes,
	}
	if err := artifact.VerifyBytes(librarySpec, libraryData); err != nil {
		return OpenH264StatusInfo(), fmt.Errorf("verify OpenH264 library: %w", err)
	}

	destPath := filepath.Join(openH264CacheRoot(), spec.libraryName)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return OpenH264StatusInfo(), fmt.Errorf("create OpenH264 cache dir: %w", err)
	}
	if err := artifact.WriteAtomic(destPath, libraryData, 0o644); err != nil {
		return OpenH264StatusInfo(), fmt.Errorf("write OpenH264 library: %w", err)
	}

	resetOpenH264State()
	status := OpenH264StatusInfo()
	if !status.Available {
		if status.Error == "" {
			status.Error = "OpenH264 installed but failed to load"
		}
		return status, errors.New(status.Error)
	}
	return status, nil
}

func openH264InstallSupported() bool {
	_, ok := openH264InstallSpecFunc(openH264Platform())
	return ok
}

func extractOpenH264Library(archiveData []byte) ([]byte, error) {
	libraryData, err := io.ReadAll(io.LimitReader(bzip2.NewReader(bytes.NewReader(archiveData)), openh264MaxLibraryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(libraryData)) > openh264MaxLibraryBytes {
		return nil, fmt.Errorf("extracted OpenH264 library exceeded %d bytes", openh264MaxLibraryBytes)
	}
	return libraryData, nil
}
