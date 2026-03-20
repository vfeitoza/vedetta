package recording

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/storage"
)

// Segment represents a recorded video file covering a time range.
type Segment struct {
	Path      string
	Camera    string
	StartTime time.Time
	EndTime   time.Time
}

// SegmentRecorder continuously records RTSP streams into fixed-length segments.
type SegmentRecorder struct {
	config     config.RecordingConfig
	baseDir    string
	db         *storage.DB
	mu         sync.RWMutex
	audioCodec map[string]string // camera name → detected audio codec (e.g. "aac", "pcm_alaw")
}

func NewSegmentRecorder(cfg config.RecordingConfig, db *storage.DB) *SegmentRecorder {
	baseDir := cfg.Path
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	return &SegmentRecorder{
		config:     cfg,
		baseDir:    baseDir,
		db:         db,
		audioCodec: make(map[string]string),
	}
}

// StartRecording begins continuous segment recording for a camera.
func (sr *SegmentRecorder) StartRecording(ctx context.Context, cameraName, rtspURL string) {
	segDir := filepath.Join(sr.baseDir, cameraName, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		slog.Error("failed to create segment directory", "camera", cameraName, "error", err)
		return
	}

	// Probe the stream's audio codec so we can decide copy vs transcode
	codec := probeAudioCodec(rtspURL)
	sr.mu.Lock()
	sr.audioCodec[cameraName] = codec
	sr.mu.Unlock()
	slog.Info("detected audio codec", "camera", cameraName, "codec", codec)

	go sr.recordLoop(ctx, cameraName, rtspURL, segDir)
}

// probeAudioCodec uses ffprobe to detect the audio codec of an RTSP stream.
// Returns the codec name (e.g. "aac", "pcm_alaw") or empty string if no audio or probe fails.
func probeAudioCodec(rtspURL string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-rtsp_transport", "tcp",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name",
		"-of", "csv=p=0",
		rtspURL,
	)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (sr *SegmentRecorder) recordLoop(ctx context.Context, cameraName, rtspURL, segDir string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Ensure segment directory exists before each recording attempt
		if err := os.MkdirAll(segDir, 0o755); err != nil {
			slog.Error("failed to ensure segment directory", "dir", segDir, "error", err)
		}

		segmentLen := sr.config.SegmentLength
		if segmentLen == 0 {
			segmentLen = 10 * time.Minute
		}

		// Align to clock boundaries: if segment length is 10m, align to :00, :10, :20, etc.
		startTime := time.Now()
		nextBoundary := startTime.Truncate(segmentLen).Add(segmentLen)
		duration := nextBoundary.Sub(startTime)
		if duration < 10*time.Second {
			// Too close to boundary, skip to the next one
			duration += segmentLen
		}

		segPath := filepath.Join(segDir, fmt.Sprintf("%s.mp4", startTime.Format("2006-01-02_15-04-05")))

		slog.Debug("starting segment", "camera", cameraName, "path", segPath)

		err := sr.recordSegment(ctx, cameraName, rtspURL, segPath, duration)

		endTime := time.Now()

		// Register the segment even if ffmpeg exited early (partial segment)
		if info, statErr := os.Stat(segPath); statErr == nil {
			sizeBefore := info.Size()

			// Remux fMP4 → regular MP4 with faststart for better playback and smaller size
			if remuxErr := remuxToFaststart(segPath); remuxErr != nil {
				slog.Warn("remux failed, keeping fragmented version", "path", segPath, "error", remuxErr)
			}

			// Re-stat after remux (size may have changed)
			if remuxInfo, err := os.Stat(segPath); err == nil {
				info = remuxInfo
			}

			rec := storage.SegmentRecord{
				Camera:    cameraName,
				Path:      segPath,
				StartTime: startTime,
				EndTime:   endTime,
				SizeBytes: info.Size(),
			}

			if dbErr := sr.db.SaveSegment(rec); dbErr != nil {
				slog.Error("failed to save segment to database", "path", segPath, "error", dbErr)
			}

			saved := sizeBefore - info.Size()
			slog.Debug("segment completed", "camera", cameraName, "path", segPath,
				"duration", endTime.Sub(startTime).Round(time.Second),
				"size", info.Size(),
				"remux_saved", saved)
		}

		if err != nil {
			slog.Error("segment recording error, retrying",
				"camera", cameraName,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (sr *SegmentRecorder) recordSegment(ctx context.Context, cameraName, rtspURL, outputPath string, duration time.Duration) error {
	segCtx, cancel := context.WithTimeout(ctx, duration+30*time.Second)
	defer cancel()

	// Use -c:a copy when audio is already AAC (saves CPU), otherwise transcode
	sr.mu.RLock()
	codec := sr.audioCodec[cameraName]
	sr.mu.RUnlock()
	audioCodecArg := "aac"
	if codec == "aac" {
		audioCodecArg = "copy"
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-rtsp_transport", "tcp",
		"-use_wallclock_as_timestamps", "1",
		"-i", rtspURL,
		"-t", fmt.Sprintf("%.0f", duration.Seconds()),
		"-c:v", "copy",
		"-c:a", audioCodecArg,
		"-movflags", "frag_keyframe+empty_moov",
		"-y",
		outputPath,
	}

	cmd := exec.CommandContext(segCtx, "ffmpeg", args...)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg segment: %w: %s", err, string(output))
	}

	return nil
}

// remuxToFaststart re-wraps a fragmented MP4 into a regular MP4 with the moov atom
// at the front (faststart). This reduces file size (~10-15%) and enables instant playback.
func remuxToFaststart(path string) error {
	tmpPath := path + ".remux.mp4"

	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-i", path,
		"-c", "copy",
		"-movflags", "+faststart",
		"-y",
		tmpPath,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg remux: %w: %s", err, string(output))
	}

	// Atomic replace: rename tmp over the original
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename remuxed file: %w", err)
	}

	return nil
}

// FindSegments returns segments for a camera that overlap the given time range.
func (sr *SegmentRecorder) FindSegments(cameraName string, from, to time.Time) []Segment {
	records, err := sr.db.QuerySegments(cameraName, from, to)
	if err != nil {
		slog.Error("failed to query segments from database", "camera", cameraName, "error", err)
		return nil
	}

	segments := make([]Segment, len(records))
	for i, r := range records {
		segments[i] = Segment{
			Path:      r.Path,
			Camera:    r.Camera,
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
	}
	return segments
}

// RemoveSegment deletes a segment file and removes it from the database.
func (sr *SegmentRecorder) RemoveSegment(cameraName string, segPath string) error {
	if err := os.Remove(segPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := sr.db.DeleteSegment(segPath); err != nil {
		return fmt.Errorf("delete segment from database: %w", err)
	}
	return nil
}

// AllSegments returns all segments for a given camera.
func (sr *SegmentRecorder) AllSegments(cameraName string) []Segment {
	records, err := sr.db.GetAllSegments(cameraName)
	if err != nil {
		slog.Error("failed to get all segments from database", "camera", cameraName, "error", err)
		return nil
	}

	segments := make([]Segment, len(records))
	for i, r := range records {
		segments[i] = Segment{
			Path:      r.Path,
			Camera:    r.Camera,
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
	}
	return segments
}

// ScanExistingSegments reconciles the filesystem with the database for a camera.
// It inserts any .mp4 files found on disk but missing from the DB, and removes
// any DB records whose files no longer exist on disk.
func (sr *SegmentRecorder) ScanExistingSegments(cameraName, segDir string) {
	slog.Info("scanning existing segments", "camera", cameraName, "dir", segDir)

	// Clean up stale remux temp files from interrupted shutdowns
	tempEntries, _ := os.ReadDir(segDir)
	for _, entry := range tempEntries {
		if strings.HasSuffix(entry.Name(), ".remux.mp4") {
			tmpPath := filepath.Join(segDir, entry.Name())
			slog.Warn("removing stale remux temp file", "path", tmpPath)
			os.Remove(tmpPath)
		}
	}

	// Build set of files on disk
	diskFiles := make(map[string]os.FileInfo)
	entries, err := os.ReadDir(segDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read segment directory", "dir", segDir, "error", err)
		}
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		fullPath := filepath.Join(segDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		diskFiles[fullPath] = info
	}

	// Check DB records against disk: remove orphans
	dbRecords, err := sr.db.GetAllSegments(cameraName)
	if err != nil {
		slog.Error("failed to query segments from database", "camera", cameraName, "error", err)
		return
	}

	dbPaths := make(map[string]bool, len(dbRecords))
	for _, rec := range dbRecords {
		dbPaths[rec.Path] = true
		if _, exists := diskFiles[rec.Path]; !exists {
			slog.Warn("removing orphaned segment record", "camera", cameraName, "path", rec.Path)
			if err := sr.db.DeleteSegment(rec.Path); err != nil {
				slog.Error("failed to delete orphaned segment", "path", rec.Path, "error", err)
			}
		}
	}

	// Check disk files against DB: insert missing
	for path, info := range diskFiles {
		if dbPaths[path] {
			continue
		}

		duration := probeDuration(path)
		startTime := info.ModTime().Add(-duration)

		rec := storage.SegmentRecord{
			Camera:    cameraName,
			Path:      path,
			StartTime: startTime,
			EndTime:   info.ModTime(),
			SizeBytes: info.Size(),
		}

		slog.Info("importing segment from disk", "camera", cameraName, "path", path)
		if err := sr.db.SaveSegment(rec); err != nil {
			slog.Error("failed to import segment", "path", path, "error", err)
		}
	}
}

// probeDuration uses ffprobe to determine the duration of a video file.
func probeDuration(path string) time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0
	}

	var seconds float64
	if _, err := fmt.Sscanf(result.Format.Duration, "%f", &seconds); err != nil {
		return 0
	}

	return time.Duration(seconds * float64(time.Second))
}
