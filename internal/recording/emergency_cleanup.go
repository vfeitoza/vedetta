package recording

import (
	"context"
	"log/slog"
	"time"

	"github.com/rvben/vedetta/internal/config"
)

// EmergencyDelete drops the oldest segments in DB order — but never anything
// younger than cfg.MinRetention — until either cfg.BatchSize segments have
// been removed or disk free space has crossed 150% of the configured minimum.
//
// Unlike runCleanup, this intentionally breaks the retain_days contract when
// the alternative is silent data loss from a paused recorder. Returns the
// number of segments actually deleted.
func (r *Recorder) EmergencyDelete(ctx context.Context, cfg config.UrgentCleanupConfig) (int, error) {
	if !cfg.Enabled {
		return 0, nil
	}

	floor := time.Now().Add(-cfg.MinRetention)
	candidates, err := r.db.GetOldestSegmentsOlderThan(cfg.BatchSize, floor)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		slog.Warn("emergency cleanup: nothing to delete above retention floor",
			"min_retention", cfg.MinRetention,
		)
		return 0, nil
	}

	minRequired := r.segments.Disk().MinRequired()
	target := minRequired * 3 / 2 // 150% of threshold
	diskWasLow := r.segments.DiskAvailable() < minRequired
	deleted := 0

	for _, seg := range candidates {
		if ctx.Err() != nil {
			break
		}
		slog.Warn("emergency cleanup: deleting segment below retention window",
			"camera", seg.Camera,
			"path", seg.Path,
			"age", time.Since(seg.EndTime).Round(time.Minute),
			"size_mb", seg.SizeBytes/(1024*1024),
		)
		if err := r.segments.RemoveSegment(seg.Camera, seg.Path); err != nil {
			slog.Error("emergency cleanup: remove failed", "path", seg.Path, "error", err)
			continue
		}
		deleted++

		// Only apply the disk-recovery early-exit when disk was critically
		// low at the start of this pass. If it wasn't (e.g. in tests or when
		// called for other reasons), exhaust the full candidate batch.
		if diskWasLow {
			if avail := r.segments.DiskAvailable(); avail > target {
				slog.Info("emergency cleanup: disk recovered",
					"available_mb", avail/(1024*1024),
					"target_mb", target/(1024*1024),
					"deleted", deleted,
				)
				break
			}
		}
	}

	slog.Warn("emergency cleanup: completed",
		"deleted", deleted,
		"retention_floor", cfg.MinRetention,
	)
	return deleted, nil
}
