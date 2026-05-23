package recording

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/rvben/vedetta/internal/storage"
)

// StartRetentionCleanup runs a background goroutine that periodically
// removes recordings older than the configured retention period.
// When disk space is critically low, cleanup runs every 30 seconds
// instead of every hour to recover space as quickly as possible.
// If normal age-based cleanup is still not enough to free space,
// EmergencyDelete is invoked: it drops the oldest segments regardless
// of retain_days, preserving only the last UrgentCleanup.MinRetention
// of recordings. This prevents the recorder from silently pausing when
// disk fills completely.
func (r *Recorder) StartRetentionCleanup(ctx context.Context) {
	go func() {
		r.runCleanup()

		normalTicker := time.NewTicker(1 * time.Hour)
		urgentTicker := time.NewTicker(30 * time.Second)
		defer normalTicker.Stop()
		defer urgentTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-normalTicker.C:
				r.runCleanup()
			case <-urgentTicker.C:
				threshold := r.segments.Disk().MinRequired()
				avail := r.segments.DiskAvailable()
				if avail >= threshold {
					continue
				}
				slog.Warn("urgent retention cleanup triggered by low disk space",
					"available_mb", avail/(1024*1024),
					"threshold_mb", threshold/(1024*1024),
				)
				r.runUrgentCleanup(ctx)
			}
		}
	}()
}

// runCleanup acquires segmentOpMu and runs one retention pass.
// The periodic loop calls this. Callers that already hold
// segmentOpMu must call runCleanupLocked directly instead.
func (r *Recorder) runCleanup() {
	r.segmentOpMu.Lock()
	defer r.segmentOpMu.Unlock()
	r.runCleanupLocked()
}

// runUrgentCleanup holds segmentOpMu for the entire urgent-cleanup sequence:
// a normal retention pass followed by an optional emergency deletion. Holding
// a single lock across both calls prevents another goroutine from writing a
// new segment between the cleanup pass and the disk recheck that determines
// whether emergency deletion is needed.
func (r *Recorder) runUrgentCleanup(ctx context.Context) {
	r.segmentOpMu.Lock()
	defer r.segmentOpMu.Unlock()

	r.runCleanupLocked()

	// Re-check after normal cleanup. If still below threshold,
	// escalate to floor-breaking emergency deletion.
	threshold := r.segments.Disk().MinRequired()
	if r.segments.DiskAvailable() >= threshold || !r.config.UrgentCleanup.Enabled {
		return
	}

	n, err := r.emergencyDeleteLocked(ctx, r.config.UrgentCleanup)
	if err != nil {
		slog.Error("emergency cleanup failed", "error", err)
		return
	}
	if n > 0 {
		slog.Warn("emergency cleanup freed space by dropping segments below retain_days",
			"segments_deleted", n,
			"min_retention", r.config.UrgentCleanup.MinRetention,
		)
	}
}

// runCleanupLocked is the actual retention body. Caller MUST hold
// r.segmentOpMu.
func (r *Recorder) runCleanupLocked() {
	slog.Debug("running retention cleanup")

	now := time.Now()

	// A retain value of 0 (or negative) means "keep forever". Age-based cleanup
	// runs only when its retain window is positive; otherwise computing
	// now.Add(-0) == now would expire and delete every record.
	r.cleanSegments()
	if r.config.EventRetain > 0 {
		eventCutoff := now.Add(-time.Duration(r.config.EventRetain) * 24 * time.Hour)
		r.cleanClips(eventCutoff)
		r.cleanSnapshots(eventCutoff)
	}
	if r.eventConfig.RetainDays > 0 {
		eventMetadataCutoff := now.Add(-time.Duration(r.eventConfig.RetainDays) * 24 * time.Hour)
		r.cleanEventMetadata(eventMetadataCutoff)
	}
	if r.config.RetainDays > 0 {
		segmentCutoff := now.Add(-time.Duration(r.config.RetainDays) * 24 * time.Hour)
		r.cleanMotionActivity(segmentCutoff)
	}
	r.reconcileEventMediaAvailability()
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

// cleanSegments removes expired segments. Each camera uses its per-camera
// retain_days override if configured; otherwise the global RetainDays applies.
//
// Per-camera overrides can extend OR shorten retention relative to the global.
// When extending (e.g. global=7, cam=30), segments selected by the global
// query are filtered out for that camera. When shortening (e.g. global=7,
// cam=1), an additional per-camera query catches segments the global query
// would miss.
func (r *Recorder) cleanSegments() {
	now := time.Now()

	// A global retain_days of 0 (or negative) means "keep forever": the
	// global expiry query is skipped entirely. Explicit per-camera overrides
	// (always > 0) still apply so a camera can opt into shorter retention even
	// when the global default keeps everything.
	globalEnabled := r.config.RetainDays > 0
	globalCutoff := now.Add(-time.Duration(r.config.RetainDays) * 24 * time.Hour)

	var expired []storage.SegmentRecord
	if globalEnabled {
		var err error
		expired, err = r.db.GetSegmentsEndingBefore(globalCutoff)
		if err != nil {
			slog.Error("failed to query expired segments", "error", err)
			return
		}
	}

	// Per-camera overrides: pick up segments older than the per-camera cutoff.
	// When the global query is enabled, only cameras with shorter-than-global
	// retention add new rows (longer overrides are filtered below). When the
	// global query is disabled, every override drives its own deletions.
	for cam, days := range r.cameraRetention {
		camCutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		if globalEnabled && !camCutoff.After(globalCutoff) {
			continue // override is >= global; the global query already covered it
		}
		more, err := r.db.GetSegmentsEndingBeforeForCamera(cam, camCutoff)
		if err != nil {
			slog.Error("failed to query expired segments for camera", "camera", cam, "error", err)
			continue
		}
		expired = append(expired, more...)
	}

	if len(expired) == 0 {
		return
	}

	slog.Debug("retention cleanup: removing expired segments", "count", len(expired))
	seen := make(map[string]struct{}, len(expired))
	for _, seg := range expired {
		if _, dup := seen[seg.Path]; dup {
			continue
		}
		seen[seg.Path] = struct{}{}

		// For cameras with longer-than-global retention, the global query may
		// have included segments that should still be kept.
		if days, ok := r.cameraRetention[seg.Camera]; ok {
			camCutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
			if seg.EndTime.After(camCutoff) {
				continue
			}
		}

		slog.Debug("removing expired segment",
			"camera", seg.Camera,
			"path", seg.Path,
			"age", time.Since(seg.EndTime).Round(time.Hour),
		)
		if err := r.segments.RemoveSegment(seg.Camera, seg.Path); err != nil {
			slog.Error("failed to remove segment", "path", seg.Path, "error", err)
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

func (r *Recorder) cleanSnapshots(cutoff time.Time) {
	cleanSnapshotDir(r.snapshotPath, cutoff)
	cleanSnapshotDir(r.snapshotFallbackPath, cutoff)
}

// cleanSnapshotDir removes .jpg files older than cutoff from dir. It is a
// no-op when dir is empty or does not exist.
func cleanSnapshotDir(dir string, cutoff time.Time) {
	if dir == "" {
		return
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jpg" {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			slog.Debug("removing expired snapshot", "path", path)
			_ = os.Remove(path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		slog.Error("error walking snapshots directory", "path", dir, "error", err)
	}
}

func (r *Recorder) cleanEventMetadata(cutoff time.Time) {
	if err := r.db.DeleteFacesOlderThan(cutoff); err != nil {
		slog.Error("failed to delete expired faces", "error", err)
	}
	if err := r.db.DeleteEventsOlderThan(cutoff); err != nil {
		slog.Error("failed to delete expired events", "error", err)
	}
}

func (r *Recorder) cleanMotionActivity(cutoff time.Time) {
	if err := r.db.DeleteMotionActivityBefore(cutoff); err != nil {
		slog.Error("failed to delete expired motion activity", "error", err)
	}
}

func (r *Recorder) reconcileEventMediaAvailability() {
	events, err := r.db.EventsWithSnapshots()
	if err != nil {
		slog.Error("failed to query events for media reconciliation", "error", err)
		return
	}

	for _, ev := range events {
		snapshotAvailable := ev.SnapshotPath != ""
		if snapshotAvailable {
			if _, err := os.Stat(ev.SnapshotPath); err != nil {
				snapshotAvailable = false
			}
		}
		if err := r.db.UpdateEventSnapshotAvailability(ev.ID, snapshotAvailable); err != nil {
			slog.Error("failed to update snapshot availability", "id", ev.ID, "error", err)
		}

		clipAvailable := ev.ClipPath != ""
		if clipAvailable {
			if _, err := os.Stat(ev.ClipPath); err != nil {
				clipAvailable = false
			}
		}
		if err := r.db.UpdateEventClipAvailability(ev.ID, clipAvailable); err != nil {
			slog.Error("failed to update clip availability", "id", ev.ID, "error", err)
		}
	}
}

// cleanEmptyDirs removes empty directories left after cleanup,
// but preserves structural directories used by active recording (e.g. "segments").
func (r *Recorder) cleanEmptyDirs() {
	r.removeEmptyDirsIn(r.config.Path)
	if r.snapshotPath != "" {
		r.removeEmptyDirsIn(r.snapshotPath)
	}
}

func (r *Recorder) removeEmptyDirsIn(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == root {
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
