package recording

import (
	"context"
	"fmt"
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

	// transcodeFn performs the actual transcoding. Defaults to media.TranscodeSegment.
	transcodeFn func(path string, targetW, targetH int) (media.TranscodeResult, error)

	// lock is the shared segment-operation mutex from the owning Recorder.
	// processOne holds it across the transcode+DB-update pair so that
	// retention cleanup and emergency delete cannot race with an in-flight
	// recompression.
	lock *sync.Mutex

	isRunning            atomic.Bool
	mu                   sync.Mutex
	lastRun              time.Time
	segmentsRecompressed int64
	bytesReclaimed       int64
}

// NewRecompressor creates a Recompressor with the given config and camera list.
// lock is the shared segmentOpMu from the owning Recorder and must not be nil.
func NewRecompressor(cfg config.TieredStorageConfig, cameras []config.CameraConfig, db *storage.DB, lock *sync.Mutex) *Recompressor {
	return &Recompressor{cfg: cfg, cameras: cameras, db: db, lock: lock, transcodeFn: media.TranscodeSegment}
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
	// Give segments stuck at the 3-failure cap another chance. A common
	// reason for stuck failures is a transiently missing codec (e.g.
	// OpenH264 wasn't installed yet during earlier recompression passes);
	// once the environment is restored, we should retry rather than
	// permanently excluding every segment that was attempted during the
	// broken period.
	if reset, err := r.db.ResetStuckRecompressFailures(); err != nil {
		slog.Warn("recompression: failed to reset stuck failures", "error", err)
	} else if reset > 0 {
		slog.Info("recompression: reset stuck failure counters", "segments", reset)
	}

	go func() {
		interval := r.cfg.Interval
		if interval <= 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
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

// safeTranscode calls media.TranscodeSegment with panic recovery.
// The OpenH264 purego bindings can panic on certain corrupt segments;
// catching the panic here prevents a single bad segment from crashing
// the entire process.
func (r *Recompressor) safeTranscode(path string) (result media.TranscodeResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic during transcode: %v", p)
		}
	}()
	return r.transcodeFn(path, r.cfg.TargetWidth, r.cfg.TargetHeight)
}

// processOne picks the single best eligible segment across all enabled cameras and transcodes it.
// When priority is "largest" (default), picks the segment with the most bytes to reclaim.
// When priority is "oldest", picks the segment with the earliest end time.
// Returns true if a segment was processed, false if nothing was eligible.
func (r *Recompressor) processOne() bool {
	now := time.Now()

	priority := r.cfg.Priority
	if priority == "" {
		priority = "largest"
	}

	var bestSeg *storage.SegmentRecord
	for camName, eff := range r.eligibleCameras() {
		cutoff := now.Add(-time.Duration(eff.AfterDays) * 24 * time.Hour)

		var segs []storage.SegmentRecord
		var err error
		if priority == "largest" {
			segs, err = r.db.GetRecompressionCandidatesBySize(camName, cutoff, 1)
		} else {
			segs, err = r.db.GetSegmentsForRecompression(camName, cutoff)
		}
		if err != nil {
			slog.Warn("recompression: query failed", "camera", camName, "error", err)
			continue
		}
		if len(segs) == 0 {
			continue
		}
		candidate := segs[0]

		if bestSeg == nil {
			bestSeg = &candidate
			continue
		}
		if priority == "largest" {
			if candidate.SizeBytes > bestSeg.SizeBytes {
				bestSeg = &candidate
			}
		} else {
			if candidate.EndTime.Before(bestSeg.EndTime) {
				bestSeg = &candidate
			}
		}
	}

	if bestSeg == nil {
		return false
	}

	if media.HLSPathInUse(bestSeg.Path) {
		slog.Debug("recompression: skipping in-use segment", "path", bestSeg.Path)
		return false
	}

	// Acquire the shared segment-operation lock before touching the file or
	// the DB row. TryLock is intentional: the recompressor is a background
	// optimization and it is preferable to skip a cycle rather than block a
	// user-initiated delete or urgent cleanup that may be waiting.
	if !r.lock.TryLock() {
		slog.Debug("recompression: skipping (another segment operation in flight)", "path", bestSeg.Path)
		return false
	}
	defer r.lock.Unlock()

	start := time.Now()
	result, err := r.safeTranscode(bestSeg.Path)
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
