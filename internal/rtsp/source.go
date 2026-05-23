package rtsp

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/backoff"
)

// Source wraps a gortsplib RTSP client, providing reconnection and consumer fan-out.
type Source struct {
	url string

	mu         sync.RWMutex
	consumers  []Consumer
	videoTrack *TrackInfo
	audioTrack *TrackInfo
	connected  bool

	// paramsReady is closed once videoTrack holds a usable SPS+PPS pair, so
	// WaitForVideoParams can block on a notification instead of polling.
	paramsReady chan struct{}
}

// NewSource creates a new RTSP source for the given URL.
func NewSource(url string) *Source {
	return &Source{url: url, paramsReady: make(chan struct{})}
}

// signalParamsReadyLocked closes paramsReady the first time the video track has
// a usable SPS+PPS pair. The caller must hold s.mu.
func (s *Source) signalParamsReadyLocked() {
	if s.videoTrack == nil || len(s.videoTrack.SPS) < 4 || len(s.videoTrack.PPS) == 0 {
		return
	}
	select {
	case <-s.paramsReady:
		// already closed
	default:
		close(s.paramsReady)
	}
}

// URL returns the RTSP URL of this source.
func (s *Source) URL() string {
	return s.url
}

// AddConsumer registers a consumer to receive RTP packets.
func (s *Source) AddConsumer(c Consumer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consumers = append(s.consumers, c)
}

// RemoveConsumer unregisters a consumer.
func (s *Source) RemoveConsumer(c Consumer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.consumers {
		if existing == c {
			s.consumers = append(s.consumers[:i], s.consumers[i+1:]...)
			break
		}
	}
}

// ConsumerCount returns the number of active consumers.
func (s *Source) ConsumerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.consumers)
}

// VideoTrack returns the video track info, or nil if not yet connected.
func (s *Source) VideoTrack() *TrackInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.videoTrack
}

// AudioTrack returns the audio track info, or nil if not available.
func (s *Source) AudioTrack() *TrackInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.audioTrack
}

// SetVideoTrack sets the video track info (for testing).
func (s *Source) SetVideoTrack(ti *TrackInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videoTrack = ti
	s.signalParamsReadyLocked()
}

// SetAudioTrack sets the audio track info (for testing).
func (s *Source) SetAudioTrack(ti *TrackInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioTrack = ti
}

// Connected returns whether the source is currently connected.
func (s *Source) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// WaitForVideoParams blocks until the source has cached an SPS+PPS pair for the
// video track, or until ctx is done. Returns true on success.
//
// Cameras that omit sprop-parameter-sets from their RTSP SDP (e.g. some
// Reolink/Foscam doorbells) only advertise SPS/PPS in-band, which means the
// initial DESCRIBE leaves videoTrack.SPS empty. Negotiating a WebRTC answer
// without an SPS forces vedetta to fall back to a default profile-level-id;
// when the in-band bitstream uses a different profile, Chrome configures the
// wrong decoder and drops every frame. Blocking the offer until in-band
// parameter sets are sniffed lets us advertise a profile that actually matches
// what the camera is about to send.
func (s *Source) WaitForVideoParams(ctx context.Context) bool {
	s.mu.RLock()
	ready := s.videoTrack != nil && len(s.videoTrack.SPS) >= 4 && len(s.videoTrack.PPS) > 0
	s.mu.RUnlock()
	if ready {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-s.paramsReady:
		return true
	}
}

// Connect starts reading from the RTSP stream, reconnecting on failure.
// Blocks until ctx is cancelled.
func (s *Source) Connect(ctx context.Context) {
	b := 5 * time.Second
	const maxBackoff = 2 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := s.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			// Successful connection ended cleanly (e.g., server closed).
			// Reset backoff for quick reconnect.
			b = time.Second
		}

		if err != nil {
			slog.Error("RTSP connection error, reconnecting",
				"url", SanitizeURL(s.url),
				"error", err,
				"retry_in", b,
			)
		} else {
			slog.Info("RTSP connection closed, reconnecting", "url", SanitizeURL(s.url))
		}

		s.notifyDisconnect()

		// Jitter the wait so a fleet of sources that drop together (NVR restart,
		// switch reboot) does not reconnect in lockstep.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff.Jitter(b, rand.Float64())):
		}

		b = time.Duration(float64(b) * 1.5)
		if b > maxBackoff {
			b = maxBackoff
		}
	}
}

func (s *Source) notifyDisconnect() {
	s.mu.Lock()
	s.connected = false
	consumers := make([]Consumer, len(s.consumers))
	copy(consumers, s.consumers)
	s.mu.Unlock()

	for _, c := range consumers {
		c.OnDisconnect()
	}
}

func (s *Source) connectOnce(ctx context.Context) error {
	u, err := base.ParseURL(s.url)
	if err != nil {
		return err
	}

	proto := gortsplib.ProtocolTCP
	client := &gortsplib.Client{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Protocol: &proto,
	}

	if err := client.Start(); err != nil {
		return err
	}
	defer client.Close()

	desc, _, err := client.Describe(u)
	if err != nil {
		return err
	}

	s.extractTracks(desc)

	if err := client.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		return err
	}

	// Register a single RTP handler that dispatches by media type.
	// OnPacketRTPAny sets one global handler — calling it in a loop
	// would replace the previous handler on each iteration.
	client.OnPacketRTPAny(func(medi *description.Media, _ format.Format, pkt *rtp.Packet) {
		if medi.Type == description.MediaTypeVideo {
			s.fanOutVideo(pkt)
		} else {
			s.fanOutAudio(pkt)
		}
	})

	if _, err := client.Play(nil); err != nil {
		return err
	}

	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()

	slog.Info("RTSP connected", "url", SanitizeURL(s.url))

	// Wait blocks until the client encounters a fatal error or is closed
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- client.Wait()
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-waitDone:
		return err
	}
}

func (s *Source) extractTracks(desc *description.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			switch f := forma.(type) {
			case *format.H264:
				ti := &TrackInfo{
					Codec:       "H264",
					ClockRate:   f.ClockRate(),
					IsVideo:     true,
					PayloadType: f.PayloadType(),
				}
				if f.SPS != nil {
					ti.SPS = make([]byte, len(f.SPS))
					copy(ti.SPS, f.SPS)
				}
				if f.PPS != nil {
					ti.PPS = make([]byte, len(f.PPS))
					copy(ti.PPS, f.PPS)
				}
				s.videoTrack = ti

			case *format.MPEG4Audio:
				channels := 1
				if f.Config != nil && f.Config.ChannelConfig > 0 {
					channels = int(f.Config.ChannelConfig)
					if channels == 7 {
						channels = 8
					}
				}
				s.audioTrack = &TrackInfo{
					Codec:        "AAC",
					ClockRate:    f.ClockRate(),
					PayloadType:  f.PayloadType(),
					ChannelCount: channels,
				}

			case *format.G711:
				codec := "PCMU"
				if !f.MULaw {
					codec = "PCMA"
				}
				s.audioTrack = &TrackInfo{
					Codec:        codec,
					ClockRate:    f.ClockRate(),
					PayloadType:  f.PayloadType(),
					ChannelCount: f.ChannelCount,
				}
			}
		}
	}
	s.signalParamsReadyLocked()
}

func (s *Source) fanOutVideo(pkt *rtp.Packet) {
	s.maybeLearnParameterSets(pkt)
	// gortsplib reuses pkt and its Payload backing array for the next
	// inbound packet. Every consumer (recording, snapshot, detection)
	// decodes asynchronously on its own goroutine, so they must not retain
	// the library-owned buffer. Hand them one immutable deep copy; consumers
	// only read it, so a single shared clone is safe.
	pkt = pkt.Clone()
	start := time.Now()
	// Snapshot the consumer set and dispatch without holding s.mu: a consumer
	// that marshals a segment or writes to a stuck peer must not block
	// AddConsumer/RemoveConsumer (a viewer attaching or detaching).
	consumers := s.snapshotConsumers()
	for _, c := range consumers {
		c.OnVideoRTP(pkt)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		slog.Warn("slow fanOutVideo", "url", SanitizeURL(s.url), "elapsed", elapsed, "consumers", len(consumers))
	}
}

// snapshotConsumers returns a copy of the current consumer set so the per-packet
// fan-out can run without holding s.mu.
func (s *Source) snapshotConsumers() []Consumer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.consumers) == 0 {
		return nil
	}
	return append([]Consumer(nil), s.consumers...)
}

// maybeLearnParameterSets scans an inbound H.264 RTP payload for SPS (NAL 7)
// and PPS (NAL 8) and caches them on videoTrack when not already present.
// Single-NAL packets and STAP-A aggregates are handled; FU-A fragments are
// ignored because reassembling them just for learning would require buffering
// across packets and parameter sets virtually never need fragmentation.
//
// Only the FIRST observed SPS/PPS wins. Updating mid-stream would invalidate
// the profile-level-id already negotiated with active peers and trigger a
// silent decoder mismatch — far worse than missing one parameter-set update.
func (s *Source) maybeLearnParameterSets(pkt *rtp.Packet) {
	if len(pkt.Payload) < 1 {
		return
	}
	var sps, pps []byte
	switch pkt.Payload[0] & 0x1f {
	case 7:
		sps = append([]byte(nil), pkt.Payload...)
	case 8:
		pps = append([]byte(nil), pkt.Payload...)
	case 24:
		offset := 1
		for offset+2 <= len(pkt.Payload) {
			size := int(pkt.Payload[offset])<<8 | int(pkt.Payload[offset+1])
			offset += 2
			if size < 1 || offset+size > len(pkt.Payload) {
				return
			}
			inner := pkt.Payload[offset : offset+size]
			switch inner[0] & 0x1f {
			case 7:
				if sps == nil {
					sps = append([]byte(nil), inner...)
				}
			case 8:
				if pps == nil {
					pps = append([]byte(nil), inner...)
				}
			}
			offset += size
		}
	}
	if sps == nil && pps == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.videoTrack == nil {
		s.videoTrack = &TrackInfo{Codec: "H264", IsVideo: true, ClockRate: 90000, PayloadType: pkt.PayloadType}
	}
	if sps != nil && len(s.videoTrack.SPS) == 0 {
		s.videoTrack.SPS = sps
	}
	if pps != nil && len(s.videoTrack.PPS) == 0 {
		s.videoTrack.PPS = pps
	}
	s.signalParamsReadyLocked()
}

func (s *Source) fanOutAudio(pkt *rtp.Packet) {
	// See fanOutVideo: audio consumers also decode asynchronously, so the
	// gortsplib-owned packet must be cloned before hand-off.
	pkt = pkt.Clone()
	start := time.Now()
	consumers := s.snapshotConsumers()
	for _, c := range consumers {
		c.OnAudioRTP(pkt)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		slog.Warn("slow fanOutAudio", "url", SanitizeURL(s.url), "elapsed", elapsed, "consumers", len(consumers))
	}
}
