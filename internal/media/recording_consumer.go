package media

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/rtsp"
)

// diskPauseRetryInterval is how often a paused consumer retries the disk check.
const diskPauseRetryInterval = 30 * time.Second

// SegmentInfo is passed to the OnSegmentDone callback when a segment is completed.
type SegmentInfo struct {
	Camera    string
	Path      string
	StartTime time.Time
	EndTime   time.Time
	SizeBytes int64
}

type rtpMsg struct {
	pkt   *rtp.Packet
	video bool
}

// RecordingConsumer implements rtsp.Consumer and writes RTP packets to fMP4 segments.
// Packets are buffered via a channel so the RTSP reader goroutine is never blocked.
type RecordingConsumer struct {
	camera     string
	segLen     time.Duration
	videoTrack *rtsp.TrackInfo
	audioTrack *rtsp.TrackInfo
	onSegment  func(SegmentInfo)
	segDir     string
	disk       *DiskSpace

	pktCh  chan rtpMsg
	done   chan struct{}
	closed atomic.Bool

	mu              sync.Mutex
	writer          *SegmentWriter
	segPath         string
	currentPath     string // path of the segment currently being written; "" when closed
	segStart        time.Time
	paused          bool
	pausedAtomic    atomic.Bool // lock-free read for external status checks
	pausedSince     time.Time
	lastDiskWarning time.Time
	writeErrors     int
}

// NewRecordingConsumer creates a consumer that records to rotating fMP4 segments.
// onSegment is called when each segment completes (for DB registration).
func NewRecordingConsumer(segDir, camera string, segLen time.Duration, video, audio *rtsp.TrackInfo, disk *DiskSpace, onSegment func(SegmentInfo)) *RecordingConsumer {
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		slog.Error("failed to create segment directory", "camera", camera, "error", err)
	}

	rc := &RecordingConsumer{
		camera:     camera,
		segLen:     segLen,
		videoTrack: video,
		audioTrack: audio,
		onSegment:  onSegment,
		segDir:     segDir,
		disk:       disk,
		pktCh:      make(chan rtpMsg, 512),
		done:       make(chan struct{}),
	}

	go rc.processLoop()

	return rc
}

// Paused returns true if recording is paused due to low disk space.
// Uses an atomic load to avoid blocking on the processing mutex.
func (rc *RecordingConsumer) Paused() bool {
	return rc.pausedAtomic.Load()
}

// OnVideoRTP enqueues a video RTP packet for async processing.
func (rc *RecordingConsumer) OnVideoRTP(pkt *rtp.Packet) {
	if rc.closed.Load() {
		return
	}
	select {
	case rc.pktCh <- rtpMsg{pkt: pkt, video: true}:
	default:
		// Drop packet if buffer full — better than blocking the RTSP reader
	}
}

// OnAudioRTP enqueues an audio RTP packet for async processing.
func (rc *RecordingConsumer) OnAudioRTP(pkt *rtp.Packet) {
	if rc.closed.Load() {
		return
	}
	select {
	case rc.pktCh <- rtpMsg{pkt: pkt, video: false}:
	default:
	}
}

// OnDisconnect is called when the RTSP source disconnects.
func (rc *RecordingConsumer) OnDisconnect() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.closeCurrentSegment()
}

// Close finalizes the current segment and stops the processing goroutine.
func (rc *RecordingConsumer) Close() {
	rc.closed.Store(true)
	close(rc.pktCh)
	<-rc.done // wait for processLoop to finish

	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.closeCurrentSegment()
}

func (rc *RecordingConsumer) processLoop() {
	defer close(rc.done)

	for msg := range rc.pktCh {
		rc.dispatch(msg)
	}
}

// dispatch processes a single packet. A corrupt or malformed stream can make
// the H264 depacketize/decode/mux path panic; recovering per-packet keeps the
// processLoop goroutine alive so one bad packet degrades this camera's
// recording instead of taking the whole process down. The recover runs while
// rc.mu is still held (its deferred Unlock is registered first, so it runs
// last), allowing the poisoned segment writer to be discarded safely; the next
// packet then starts a fresh segment.
func (rc *RecordingConsumer) dispatch(msg rtpMsg) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic while processing packet",
				"camera", rc.camera,
				"panic", r,
				"stack", string(debug.Stack()),
			)
			rc.discardWriterAfterPanic()
		}
	}()

	if rc.paused {
		rc.handlePaused()
		return
	}
	if msg.video {
		rc.processVideo(msg.pkt)
	} else {
		rc.processAudio(msg.pkt)
	}
}

// discardWriterAfterPanic drops the current segment writer after a panic so a
// half-updated GOP buffer can't trigger repeated panics. Called with rc.mu
// held. The partial segment file is best-effort closed and removed.
func (rc *RecordingConsumer) discardWriterAfterPanic() {
	if rc.writer == nil {
		return
	}
	_, _ = rc.writer.Close()
	if rc.segPath != "" {
		_ = os.Remove(rc.segPath)
	}
	rc.writer = nil
	rc.currentPath = ""
}

// handlePaused checks if disk space has recovered. Called with mu held.
func (rc *RecordingConsumer) handlePaused() {
	if time.Since(rc.pausedSince) < diskPauseRetryInterval {
		return
	}

	avail := rc.disk.Available()
	threshold := rc.disk.MinRequired()
	if avail < threshold {
		rc.pausedSince = time.Now()
		if time.Since(rc.lastDiskWarning) > time.Minute {
			slog.Warn("recording still paused, disk space low",
				"camera", rc.camera,
				"available_mb", avail/(1024*1024),
				"required_mb", threshold/(1024*1024),
			)
			rc.lastDiskWarning = time.Now()
		}
		return
	}

	slog.Info("recording resumed, disk space recovered",
		"camera", rc.camera,
		"available_mb", avail/(1024*1024),
	)
	rc.paused = false
	rc.pausedAtomic.Store(false)
	rc.writeErrors = 0
}

func (rc *RecordingConsumer) processVideo(pkt *rtp.Packet) {
	if err := rc.ensureSegment(); err != nil {
		rc.handleWriteError(err)
		return
	}

	if err := rc.writer.WriteVideo(pkt); err != nil {
		rc.handleWriteError(err)
		return
	}

	rc.writeErrors = 0
	rc.maybeRotate()
}

func (rc *RecordingConsumer) processAudio(pkt *rtp.Packet) {
	if rc.writer == nil {
		return
	}

	if err := rc.writer.WriteAudio(pkt); err != nil {
		rc.handleWriteError(err)
	}
}

// handleWriteError handles write failures. On repeated errors (likely disk full),
// it closes the segment and pauses recording. Called with mu held.
func (rc *RecordingConsumer) handleWriteError(err error) {
	rc.writeErrors++

	if rc.writeErrors >= 3 {
		slog.Error("repeated write failures, pausing recording",
			"camera", rc.camera,
			"error", err,
			"consecutive_errors", rc.writeErrors,
		)
		rc.closeCurrentSegment()
		rc.paused = true
		rc.pausedAtomic.Store(true)
		rc.pausedSince = time.Now()
		rc.lastDiskWarning = time.Now()
		return
	}

	slog.Error("write failed", "camera", rc.camera, "error", err)
}

func (rc *RecordingConsumer) ensureSegment() error {
	if rc.writer != nil {
		return nil
	}

	// Check disk space before creating a new segment
	avail := rc.disk.Available()
	threshold := rc.disk.MinRequired()
	if avail < threshold {
		if time.Since(rc.lastDiskWarning) > time.Minute {
			slog.Warn("recording paused, insufficient disk space",
				"camera", rc.camera,
				"available_mb", avail/(1024*1024),
				"required_mb", threshold/(1024*1024),
			)
			rc.lastDiskWarning = time.Now()
		}
		rc.paused = true
		rc.pausedAtomic.Store(true)
		rc.pausedSince = time.Now()
		return fmt.Errorf("insufficient disk space: %d MB available", avail/(1024*1024))
	}

	now := time.Now()
	rc.segStart = now
	rc.segPath = filepath.Join(rc.segDir, fmt.Sprintf("%s.mp4", now.Format("2006-01-02_15-04-05")))

	var err error
	rc.writer, err = NewSegmentWriter(rc.segPath, rc.videoTrack, rc.audioTrack)
	if err != nil {
		return fmt.Errorf("create segment writer: %w", err)
	}
	rc.currentPath = rc.segPath

	slog.Debug("started new segment", "camera", rc.camera, "path", rc.segPath)

	// Register segment in DB immediately so it's queryable before rotation.
	// Uses projected end time; closeCurrentSegment will update with actual values.
	if rc.onSegment != nil {
		rc.onSegment(SegmentInfo{
			Camera:    rc.camera,
			Path:      rc.segPath,
			StartTime: rc.segStart,
			EndTime:   rc.segStart.Add(rc.segLen),
			SizeBytes: 0,
		})
	}

	return nil
}

func (rc *RecordingConsumer) maybeRotate() {
	if time.Since(rc.segStart) < rc.segLen {
		return
	}
	rc.closeCurrentSegment()
}

func (rc *RecordingConsumer) closeCurrentSegment() {
	if rc.writer == nil {
		return
	}

	duration, err := rc.writer.Close()
	if err != nil {
		slog.Error("close segment failed", "camera", rc.camera, "error", err)
	}

	if info, err := os.Stat(rc.segPath); err == nil && info.Size() > 0 {
		if rc.onSegment != nil {
			rc.onSegment(SegmentInfo{
				Camera:    rc.camera,
				Path:      rc.segPath,
				StartTime: rc.segStart,
				EndTime:   rc.segStart.Add(duration),
				SizeBytes: info.Size(),
			})
		}
		slog.Debug("segment completed", "camera", rc.camera, "path", rc.segPath,
			"duration", duration.Round(time.Second), "size", info.Size())
	} else {
		os.Remove(rc.segPath)
	}

	rc.writer = nil
	rc.currentPath = ""
}

// Camera returns the camera name this consumer records for.
func (rc *RecordingConsumer) Camera() string {
	return rc.camera
}

// CurrentSegmentPath returns the absolute path of the segment currently
// being written, or "" if no segment is open. Safe for concurrent use.
func (rc *RecordingConsumer) CurrentSegmentPath() string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.currentPath
}

// SetTestState seeds the consumer's identity and open-path fields for
// tests. Do not call from production code.
func (rc *RecordingConsumer) SetTestState(camera, path string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.camera = camera
	rc.currentPath = path
}
