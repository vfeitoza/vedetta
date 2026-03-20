package recording

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// StartRetentionCleanup runs a background goroutine that periodically
// removes recordings older than the configured retention period.
func (r *Recorder) StartRetentionCleanup(ctx context.Context) {
	go func() {
		// Run cleanup on startup, then every hour
		r.runCleanup()

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.runCleanup()
			}
		}
	}()
}

func (r *Recorder) runCleanup() {
	slog.Debug("running retention cleanup")

	segmentCutoff := time.Now().Add(-time.Duration(r.config.RetainDays) * 24 * time.Hour)
	eventCutoff := time.Now().Add(-time.Duration(r.config.EventRetain) * 24 * time.Hour)

	r.cleanSegments(segmentCutoff)
	r.cleanClips(eventCutoff)
	r.enforceStorageCap()
	r.cleanEmptyDirs()
}

// enforceStorageCap deletes the oldest segments until total storage is under the configured cap.
func (r *Recorder) enforceStorageCap() {
	cap := r.config.MaxStorageBytes()
	if cap <= 0 {
		return
	}

	totalBytes, err := r.db.TotalStorageBytes()
	if err != nil {
		slog.Error("failed to query total storage for cap enforcement", "error", err)
		return
	}

	if totalBytes <= cap {
		return
	}

	slog.Warn("storage exceeds cap, removing oldest segments",
		"total", totalBytes,
		"cap", cap,
	)

	// Fetch oldest segments in batches rather than loading all per camera.
	for totalBytes > cap {
		oldest, err := r.db.GetOldestSegments(20)
		if err != nil {
			slog.Error("failed to query oldest segments", "error", err)
			return
		}
		if len(oldest) == 0 {
			return
		}

		for _, seg := range oldest {
			if totalBytes <= cap {
				return
			}

			slog.Debug("removing segment for storage cap",
				"camera", seg.Camera,
				"path", seg.Path,
			)
			if err := r.segments.RemoveSegment(seg.Camera, seg.Path); err != nil {
				slog.Error("failed to remove segment", "path", seg.Path, "error", err)
				continue
			}
			totalBytes -= seg.SizeBytes
		}
	}
}

func (r *Recorder) cleanSegments(cutoff time.Time) {
	for _, cameraName := range r.listCameras() {
		segments := r.segments.AllSegments(cameraName)
		for _, seg := range segments {
			if seg.EndTime.Before(cutoff) {
				slog.Debug("removing expired segment",
					"camera", cameraName,
					"path", seg.Path,
					"age", time.Since(seg.EndTime).Round(time.Hour),
				)
				if err := r.segments.RemoveSegment(cameraName, seg.Path); err != nil {
					slog.Error("failed to remove segment", "path", seg.Path, "error", err)
				}
			}
		}
	}
}

func (r *Recorder) cleanClips(cutoff time.Time) {
	clipsBase := r.config.Path

	// Walk clips directories and remove files older than the cutoff
	err := filepath.Walk(clipsBase, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if info.IsDir() {
			return nil
		}

		// Only clean clip files (not segments)
		if filepath.Ext(path) != ".mp4" {
			return nil
		}

		// Check if this is in a "clips" directory
		dir := filepath.Dir(path)
		if filepath.Base(filepath.Dir(dir)) != "clips" {
			return nil
		}

		if info.ModTime().Before(cutoff) {
			slog.Debug("removing expired clip", "path", path, "age", time.Since(info.ModTime()).Round(time.Hour))
			if err := os.Remove(path); err != nil {
				slog.Warn("failed to remove expired clip", "path", path, "error", err)
			}
		}

		return nil
	})

	if err != nil {
		slog.Error("error walking clips directory", "error", err)
	}
}

// cleanEmptyDirs removes empty directories left after cleanup,
// but preserves structural directories used by active recording (e.g. "segments").
func (r *Recorder) cleanEmptyDirs() {
	_ = filepath.Walk(r.config.Path, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == r.config.Path {
			return nil
		}

		// Never remove structural directories used by active cameras
		base := filepath.Base(path)
		if base == "segments" || base == "clips" {
			return nil
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		if len(entries) == 0 {
			slog.Debug("removing empty directory", "path", path)
			_ = os.Remove(path)
		}

		return nil
	})
}

func (r *Recorder) listCameras() []string {
	var cameras []string
	entries, err := os.ReadDir(r.config.Path)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			cameras = append(cameras, entry.Name())
		}
	}
	return cameras
}
