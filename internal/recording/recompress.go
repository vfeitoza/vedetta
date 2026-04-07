package recording

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/storage"
)

// Recompressor runs scheduled overnight transcoding of old segments.
type Recompressor struct {
	cfg     config.TieredStorageConfig
	cameras []config.CameraConfig
	db      *storage.DB

	isRunning            atomic.Bool
	mu                   sync.Mutex
	lastRun              time.Time
	segmentsRecompressed int64
	bytesReclaimed       int64
}

// NewRecompressor creates a Recompressor with the given config and camera list.
func NewRecompressor(cfg config.TieredStorageConfig, cameras []config.CameraConfig, db *storage.DB) *Recompressor {
	return &Recompressor{cfg: cfg, cameras: cameras, db: db}
}

// RecompressorStats holds runtime counters for the recompression job.
type RecompressorStats struct {
	LastRun              time.Time
	SegmentsRecompressed int64
	BytesReclaimed       int64
	IsRunning            bool
}

// Stats returns a snapshot of the recompressor's runtime counters.
func (r *Recompressor) Stats() RecompressorStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RecompressorStats{
		LastRun:              r.lastRun,
		SegmentsRecompressed: r.segmentsRecompressed,
		BytesReclaimed:       r.bytesReclaimed,
		IsRunning:            r.isRunning.Load(),
	}
}

// Start runs the recompression job in a background goroutine until ctx is cancelled.
// Does nothing if tiered storage is disabled.
func (r *Recompressor) Start(ctx context.Context) {
	if !r.cfg.Enabled {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				inWindow, err := config.InScheduleWindow(r.cfg.Schedule, time.Now())
				if err != nil {
					slog.Warn("recompression: invalid schedule", "schedule", r.cfg.Schedule, "error", err)
					continue
				}
				if !inWindow {
					continue
				}
				r.processOne()
			}
		}
	}()
}

// eligibleCameras returns the effective tiered storage config for each camera
// that has tiered storage enabled, keyed by camera name.
func (r *Recompressor) eligibleCameras() map[string]config.TieredStorageConfig {
	result := make(map[string]config.TieredStorageConfig)
	for _, cam := range r.cameras {
		eff := cam.EffectiveTieredStorage(r.cfg)
		if eff.Enabled {
			result[cam.Name] = eff
		}
	}
	return result
}

// RunNow runs a full recompression pass outside the schedule window.
// It returns immediately if a pass is already running.
// The pass continues until no more eligible segments remain.
func (r *Recompressor) RunNow(ctx context.Context) {
	if !r.isRunning.CompareAndSwap(false, true) {
		return
	}
	defer r.isRunning.Store(false)

	slog.Info("recompression: manual pass started")
	processed := 0
	for {
		select {
		case <-ctx.Done():
			slog.Info("recompression: manual pass cancelled", "processed", processed)
			return
		default:
		}
		if !r.processOne() {
			break
		}
		processed++
	}
	slog.Info("recompression: manual pass completed", "processed", processed)
}

// processOne picks the oldest eligible segment across all enabled cameras and transcodes it.
// Returns true if a segment was processed, false if nothing was eligible.
func (r *Recompressor) processOne() bool {
	now := time.Now()

	var bestSeg *storage.SegmentRecord
	for camName, eff := range r.eligibleCameras() {
		cutoff := now.Add(-time.Duration(eff.AfterDays) * 24 * time.Hour)
		segs, err := r.db.GetSegmentsForRecompression(camName, cutoff)
		if err != nil {
			slog.Warn("recompression: query failed", "camera", camName, "error", err)
			continue
		}
		if len(segs) == 0 {
			continue
		}
		candidate := segs[0]
		if bestSeg == nil || candidate.EndTime.Before(bestSeg.EndTime) {
			bestSeg = &candidate
		}
	}

	if bestSeg == nil {
		return false
	}

	if media.HLSPathInUse(bestSeg.Path) {
		slog.Debug("recompression: skipping in-use segment", "path", bestSeg.Path)
		return false
	}

	start := time.Now()
	result, err := media.TranscodeSegment(bestSeg.Path, r.cfg.TargetWidth, r.cfg.TargetHeight)
	if err != nil {
		slog.Warn("recompression: failed",
			"camera", bestSeg.Camera,
			"path", bestSeg.Path,
			"error", err,
			"retry", bestSeg.RecompressFailures+1,
		)
		if dbErr := r.db.IncrementSegmentRecompressFailures(bestSeg.ID); dbErr != nil {
			slog.Error("recompression: failed to increment failure count", "id", bestSeg.ID, "error", dbErr)
		}
		return true
	}

	if result.Skipped {
		// Mark as done so it is never reconsidered
		if err := r.db.MarkSegmentRecompressed(bestSeg.ID, bestSeg.SizeBytes); err != nil {
			slog.Error("recompression: failed to mark skipped segment", "id", bestSeg.ID, "error", err)
		}
		slog.Debug("recompression: skipped (already small enough)", "path", bestSeg.Path)
		return true
	}

	if err := r.db.MarkSegmentRecompressed(bestSeg.ID, result.NewSize); err != nil {
		slog.Error("recompression: failed to mark segment recompressed", "id", bestSeg.ID, "error", err)
		return true
	}

	saved := result.OriginalSize - result.NewSize
	r.mu.Lock()
	r.lastRun = time.Now()
	r.segmentsRecompressed++
	r.bytesReclaimed += saved
	r.mu.Unlock()

	slog.Info("recompression: completed",
		"camera", bestSeg.Camera,
		"path", bestSeg.Path,
		"original_mb", result.OriginalSize/(1024*1024),
		"new_mb", result.NewSize/(1024*1024),
		"saved_mb", saved/(1024*1024),
		"duration", time.Since(start).Round(time.Second),
	)
	return true
}
