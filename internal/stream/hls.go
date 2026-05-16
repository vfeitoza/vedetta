package stream

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtpmpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/rtsp"
)

const (
	// One second of 90kHz video ticks. Segments are cut on the first
	// keyframe at or after this much content, so the real segment length
	// is max(targetSegmentTicks, camera GOP). Short segments keep the
	// live edge close without LL-HLS machinery.
	hlsTargetSegmentTicks = 90000

	// Completed segments kept addressable in the rolling window. iOS holds
	// a few segments of buffer, so six gives a stable playlist with a few
	// seconds of DVR without unbounded memory growth.
	hlsWindowSegments = 6

	// A consumer with no playlist/segment/init request for this long is
	// torn down and detached from the RTSP source.
	hlsIdleTimeout = 30 * time.Second

	hlsAudioSampleDuration = 1024 // AAC frame: fixed 1024 PCM samples
)

// hlsSegment is one completed CMAF media segment: a single multi-track
// fMP4 fragment (moof+mdat) holding the video GOP and the audio that plays
// alongside it.
type hlsSegment struct {
	id       uint64
	data     []byte
	duration float64 // seconds, from the video track
	disc     bool    // true when SPS changed before this segment
}

// hlsConsumer implements rtsp.Consumer, muxing live RTP H.264+AAC into a
// rolling window of keyframe-aligned CMAF segments plus a live media
// playlist. iOS WebKit (Safari and Chrome) plays this natively in <video>.
type hlsConsumer struct {
	mu sync.Mutex

	videoSPS    []byte
	videoPPS    []byte
	h264Decoder *rtph264.Decoder

	hasAudio       bool
	aacDecoder     *rtpmpeg4audio.Decoder
	aacConfig      *mpeg4audio.AudioSpecificConfig
	audioTimeScale uint32

	initSegment      []byte
	videoReady       bool
	hasFirstKeyframe bool
	pendingDisc      bool

	// Current segment under construction.
	segVideo      []*fmp4.Sample
	segVideoTicks uint32
	segAudio      []*fmp4.Sample
	segAudioTicks uint32

	// The newest video sample is held until the next packet arrives so its
	// duration (PTS[N+1]-PTS[N]) can be filled in correctly.
	inFlightVideo    *fmp4.Sample
	inFlightVideoPTS uint32
	hasInFlight      bool

	// Per-track running DTS across all emitted segments (fragment BaseTime).
	videoDTS uint64
	audioDTS uint64
	moofSeq  uint32

	ring      []hlsSegment
	nextSegID uint64

	lastAccess atomic.Int64 // unix nanos; bumped on every HTTP serve
}

func newHLSConsumer(video, audio *rtsp.TrackInfo) *hlsConsumer {
	c := &hlsConsumer{audioTimeScale: 90000}
	c.lastAccess.Store(time.Now().UnixNano())

	if video != nil && video.Codec == "H264" {
		c.videoSPS = video.SPS
		c.videoPPS = video.PPS
		dec, err := newH264Decoder(video.SPS, video.PPS)
		if err != nil {
			return c
		}
		c.h264Decoder = dec
	}

	if setup, err := newAACSetup(audio); err == nil && setup != nil {
		c.hasAudio = true
		c.aacDecoder = setup.decoder
		c.aacConfig = setup.config
		c.audioTimeScale = setup.timeScale
	}

	return c
}

func (c *hlsConsumer) touch() {
	c.lastAccess.Store(time.Now().UnixNano())
}

func (c *hlsConsumer) idle(now time.Time) bool {
	return now.UnixNano()-c.lastAccess.Load() > int64(hlsIdleTimeout)
}

func (c *hlsConsumer) aacConfigForInit() *mpeg4audio.AudioSpecificConfig {
	if c.hasAudio {
		return c.aacConfig
	}
	return nil
}

func (c *hlsConsumer) OnVideoRTP(pkt *rtp.Packet) {
	if c.h264Decoder == nil {
		return
	}
	au, err := c.h264Decoder.Decode(pkt)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	spsChanged := false
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS:
			if !bytes.Equal(c.videoSPS, nalu) {
				c.videoSPS = nalu
				spsChanged = true
			}
		case h264.NALUTypePPS:
			c.videoPPS = nalu
		}
	}

	if c.initSegment == nil || spsChanged {
		initSeg, err := buildFMP4Init(c.videoSPS, c.videoPPS, c.aacConfigForInit(), c.audioTimeScale)
		if err != nil {
			return
		}
		c.initSegment = initSeg
		c.videoReady = true
		if spsChanged {
			// Restart segmentation; keep nextSegID/moofSeq monotonic and
			// flag the next segment as a discontinuity so the player
			// re-reads the (now updated) init segment cleanly.
			c.hasFirstKeyframe = false
			c.hasInFlight = false
			c.segVideo = nil
			c.segVideoTicks = 0
			c.segAudio = nil
			c.segAudioTicks = 0
			c.videoDTS = 0
			c.audioDTS = 0
			c.pendingDisc = true
		}
	}

	if !c.videoReady {
		return
	}

	if !c.hasFirstKeyframe {
		if !h264.IsRandomAccess(au) {
			return
		}
		c.hasFirstKeyframe = true
	}

	newSample := &fmp4.Sample{}
	if err := newSample.FillH264(0, au); err != nil {
		return
	}
	isKeyframe := h264.IsRandomAccess(au)

	// Finalize the previous in-flight sample now that its successor's PTS
	// is known, then append it to the current segment.
	if c.hasInFlight {
		delta := pkt.Timestamp - c.inFlightVideoPTS
		if delta == 0 || delta > 90000*2 {
			delta = 90000 / 30
		}
		c.inFlightVideo.Duration = delta
		c.segVideo = append(c.segVideo, c.inFlightVideo)
		c.segVideoTicks += delta
	}

	// A keyframe at or past the target length starts a new segment; the
	// keyframe itself becomes the first sample of that next segment.
	if isKeyframe && c.segVideoTicks >= hlsTargetSegmentTicks && len(c.segVideo) > 0 {
		c.closeSegmentLocked()
	}

	c.inFlightVideo = newSample
	c.inFlightVideoPTS = pkt.Timestamp
	c.hasInFlight = true
}

func (c *hlsConsumer) OnAudioRTP(pkt *rtp.Packet) {
	if c.aacDecoder == nil {
		return
	}
	aus, err := c.aacDecoder.Decode(pkt)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.videoReady || !c.hasFirstKeyframe {
		return
	}

	for _, au := range aus {
		c.segAudio = append(c.segAudio, &fmp4.Sample{
			Duration: hlsAudioSampleDuration,
			Payload:  au,
		})
		c.segAudioTicks += hlsAudioSampleDuration
	}
}

// closeSegmentLocked marshals the accumulated samples into one multi-track
// CMAF fragment, appends it to the rolling window, and resets the builders.
// Caller must hold c.mu.
func (c *hlsConsumer) closeSegmentLocked() {
	if len(c.segVideo) == 0 {
		return
	}

	tracks := []*fmp4.PartTrack{
		{
			ID:       videoTrackID,
			BaseTime: c.videoDTS,
			Samples:  c.segVideo,
		},
	}
	if c.hasAudio && len(c.segAudio) > 0 {
		tracks = append(tracks, &fmp4.PartTrack{
			ID:       audioTrackID,
			BaseTime: c.audioDTS,
			Samples:  c.segAudio,
		})
	}

	part := fmp4.Part{SequenceNumber: c.moofSeq, Tracks: tracks}
	var buf seekableBuffer
	if err := part.Marshal(&buf); err != nil {
		return
	}
	data := make([]byte, len(buf.Bytes()))
	copy(data, buf.Bytes())

	seg := hlsSegment{
		id:       c.nextSegID,
		data:     data,
		duration: float64(c.segVideoTicks) / 90000.0,
		disc:     c.pendingDisc,
	}
	c.ring = append(c.ring, seg)
	if len(c.ring) > hlsWindowSegments {
		c.ring = c.ring[len(c.ring)-hlsWindowSegments:]
	}

	c.nextSegID++
	c.moofSeq++
	c.videoDTS += uint64(c.segVideoTicks)
	c.audioDTS += uint64(c.segAudioTicks)
	c.pendingDisc = false

	c.segVideo = nil
	c.segVideoTicks = 0
	c.segAudio = nil
	c.segAudioTicks = 0
}

func (c *hlsConsumer) OnDisconnect() {}

// playlist renders the live media playlist. ok is false until at least one
// segment is ready so the handler can answer 503-and-retry instead of
// serving an empty playlist.
func (c *hlsConsumer) playlist() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.ring) == 0 {
		return "", false
	}

	maxDur := 0.0
	for i := range c.ring {
		if c.ring[i].duration > maxDur {
			maxDur = c.ring[i].duration
		}
	}
	targetDuration := int(math.Ceil(maxDur))
	if targetDuration < 1 {
		targetDuration = 1
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", targetDuration)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", c.ring[0].id)
	b.WriteString("#EXT-X-MAP:URI=\"live/init.mp4\"\n")
	for i := range c.ring {
		if c.ring[i].disc {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n", c.ring[i].duration)
		fmt.Fprintf(&b, "live/%d\n", c.ring[i].id)
	}
	return b.String(), true
}

func (c *hlsConsumer) initSeg() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initSegment == nil {
		return nil, false
	}
	out := make([]byte, len(c.initSegment))
	copy(out, c.initSegment)
	return out, true
}

func (c *hlsConsumer) segment(id uint64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.ring {
		if c.ring[i].id == id {
			return c.ring[i].data, true
		}
	}
	return nil, false
}

// HLSManager owns one hlsConsumer per RTSP URL and reaps idle ones.
type HLSManager struct {
	hub       *rtsp.Hub
	mu        sync.Mutex
	consumers map[string]*hlsConsumer
	stop      chan struct{}
	stopOnce  sync.Once
}

// NewHLSManager creates the manager and starts the idle-reaper janitor.
func NewHLSManager(hub *rtsp.Hub) *HLSManager {
	m := &HLSManager{
		hub:       hub,
		consumers: make(map[string]*hlsConsumer),
		stop:      make(chan struct{}),
	}
	go m.janitor()
	return m
}

func (m *HLSManager) janitor() {
	ticker := time.NewTicker(hlsIdleTimeout / 3)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.reapIdle(now)
		}
	}
}

func (m *HLSManager) reapIdle(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for url, c := range m.consumers {
		if !c.idle(now) {
			continue
		}
		if m.hub != nil {
			if source := m.hub.Get(url); source != nil {
				source.RemoveConsumer(c)
			}
		}
		delete(m.consumers, url)
	}
}

func (m *HLSManager) getOrCreate(rtspURL string) *hlsConsumer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.consumers[rtspURL]; ok {
		c.touch()
		return c
	}
	source := m.hub.GetOrCreate(rtspURL)
	c := newHLSConsumer(source.VideoTrack(), source.AudioTrack())
	m.consumers[rtspURL] = c
	source.AddConsumer(c)
	return c
}

// Playlist serves the live media playlist, lazily starting the consumer.
func (m *HLSManager) Playlist(rtspURL string) (string, bool) {
	c := m.getOrCreate(rtspURL)
	return c.playlist()
}

// InitSegment serves the fMP4 init segment.
func (m *HLSManager) InitSegment(rtspURL string) ([]byte, bool) {
	c := m.getOrCreate(rtspURL)
	return c.initSeg()
}

// Segment serves one media segment by its sequence id.
func (m *HLSManager) Segment(rtspURL string, id uint64) ([]byte, bool) {
	c := m.getOrCreate(rtspURL)
	return c.segment(id)
}

// Close stops the janitor and detaches every consumer from its source.
func (m *HLSManager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	defer m.mu.Unlock()
	for url, c := range m.consumers {
		if m.hub != nil {
			if source := m.hub.Get(url); source != nil {
				source.RemoveConsumer(c)
			}
		}
		delete(m.consumers, url)
	}
}
