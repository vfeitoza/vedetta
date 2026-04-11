package stream

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtpmpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/gorilla/websocket"
	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/rtsp"
)

// seekableBuffer implements io.WriteSeeker for in-memory fMP4 marshaling.
// Tracks a high-water mark so Bytes() returns only the written content,
// even if the marshaler seeks backward to patch box sizes.
type seekableBuffer struct {
	buf []byte
	pos int
	hwm int // high-water mark: max(pos) across all writes
}

func (s *seekableBuffer) Write(p []byte) (int, error) {
	end := s.pos + len(p)
	if end > len(s.buf) {
		if end > cap(s.buf) {
			newBuf := make([]byte, end, end*2)
			copy(newBuf, s.buf)
			s.buf = newBuf
		} else {
			s.buf = s.buf[:end]
		}
	}
	copy(s.buf[s.pos:], p)
	s.pos = end
	if end > s.hwm {
		s.hwm = end
	}
	return len(p), nil
}

func (s *seekableBuffer) Seek(offset int64, whence int) (int64, error) {
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = int64(s.pos) + offset
	case io.SeekEnd:
		newPos = int64(s.hwm) + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}
	if newPos < 0 {
		return 0, fmt.Errorf("negative seek position: %d", newPos)
	}
	s.pos = int(newPos)
	return newPos, nil
}

func (s *seekableBuffer) Bytes() []byte {
	return s.buf[:s.hwm]
}

const (
	mseClientChanSize = 64
	mseWriteTimeout   = 5 * time.Second
	msePingInterval   = 30 * time.Second
	mseReadTimeout    = 60 * time.Second
)

// mseClient represents a single WebSocket viewer.
type mseClient struct {
	conn *websocket.Conn
	ch   chan []byte
	done chan struct{}
}

func newMSEClient(conn *websocket.Conn) *mseClient {
	return &mseClient{
		conn: conn,
		ch:   make(chan []byte, mseClientChanSize),
		done: make(chan struct{}),
	}
}

// writePump sends queued fMP4 segments to the WebSocket client
// and sends periodic pings to detect dead connections.
func (c *mseClient) writePump() {
	ticker := time.NewTicker(msePingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case data, ok := <-c.ch:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(mseWriteTimeout))
			if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(mseWriteTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// send enqueues a binary message. Drops the frame if the client is too slow.
func (c *mseClient) send(data []byte) {
	select {
	case c.ch <- data:
	default:
	}
}

func (c *mseClient) close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

// mseConsumer implements rtsp.Consumer, muxes RTP into fMP4 and broadcasts to WebSocket clients.
type mseConsumer struct {
	mu      sync.Mutex
	clients []*mseClient

	videoSPS []byte
	videoPPS []byte
	hasAudio bool

	aacConfig      *mpeg4audio.AudioSpecificConfig
	audioTimeScale uint32

	h264Decoder *rtph264.Decoder
	aacDecoder  *rtpmpeg4audio.Decoder

	// Cached init segment — regenerated when SPS/PPS changes.
	initSegment []byte

	seqNum   uint32
	videoDTS uint64
	audioDTS uint64

	lastVideoRTP  uint32
	hasFirstVideo bool
	videoReady    bool
}

func newMSEConsumer(video, audio *rtsp.TrackInfo) *mseConsumer {
	mc := &mseConsumer{
		audioTimeScale: 90000,
	}

	if video != nil && video.Codec == "H264" {
		mc.videoSPS = video.SPS
		mc.videoPPS = video.PPS

		h264Format := &format.H264{
			PayloadTyp:        96,
			PacketizationMode: 1,
			SPS:               video.SPS,
			PPS:               video.PPS,
		}
		dec, err := h264Format.CreateDecoder()
		if err != nil {
			slog.Error("MSE: failed to create H264 depacketizer", "error", err)
			return mc
		}
		mc.h264Decoder = dec
	}

	if audio != nil && audio.Codec == "AAC" {
		mc.hasAudio = true
		mc.audioTimeScale = uint32(audio.ClockRate)

		channels := audio.ChannelCount
		if channels <= 0 {
			channels = 1
		}
		channelConfig := uint8(channels)
		if channels == 8 {
			channelConfig = 7
		}

		mc.aacConfig = &mpeg4audio.AudioSpecificConfig{
			Type:          mpeg4audio.ObjectTypeAACLC,
			SampleRate:    audio.ClockRate,
			ChannelConfig: channelConfig,
		}

		aacFormat := &format.MPEG4Audio{
			PayloadTyp: 97,
			Config: &mpeg4audio.AudioSpecificConfig{
				Type:          mpeg4audio.ObjectTypeAACLC,
				SampleRate:    audio.ClockRate,
				ChannelConfig: channelConfig,
			},
			SizeLength:       13,
			IndexLength:      3,
			IndexDeltaLength: 3,
		}
		dec, err := aacFormat.CreateDecoder()
		if err != nil {
			slog.Error("MSE: failed to create AAC depacketizer", "error", err)
			mc.hasAudio = false
			mc.aacConfig = nil
		} else {
			mc.aacDecoder = dec
		}
	}

	return mc
}

func (mc *mseConsumer) codecString() string {
	var videoCodec, audioCodec string

	if len(mc.videoSPS) >= 4 {
		videoCodec = "avc1." + hex.EncodeToString(mc.videoSPS[1:4])
	} else {
		videoCodec = "avc1.42001f"
	}

	if mc.hasAudio && mc.aacConfig != nil {
		audioCodec = "mp4a.40.2"
	}

	if audioCodec != "" {
		return fmt.Sprintf(`video/mp4; codecs="%s, %s"`, videoCodec, audioCodec)
	}
	return fmt.Sprintf(`video/mp4; codecs="%s"`, videoCodec)
}

func (mc *mseConsumer) buildInitSegment() ([]byte, error) {
	if mc.videoSPS == nil || mc.videoPPS == nil {
		return nil, fmt.Errorf("no SPS/PPS available")
	}

	init := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{
				ID:        1,
				TimeScale: 90000,
				Codec: &codecs.H264{
					SPS: mc.videoSPS,
					PPS: mc.videoPPS,
				},
			},
		},
	}

	if mc.hasAudio && mc.aacConfig != nil {
		init.Tracks = append(init.Tracks, &fmp4.InitTrack{
			ID:        2,
			TimeScale: mc.audioTimeScale,
			Codec: &codecs.MPEG4Audio{
				Config: *mc.aacConfig,
			},
		})
	}

	var buf seekableBuffer
	if err := init.Marshal(&buf); err != nil {
		return nil, fmt.Errorf("marshal init segment: %w", err)
	}
	out := make([]byte, len(buf.Bytes()))
	copy(out, buf.Bytes())
	return out, nil
}

func (mc *mseConsumer) OnVideoRTP(pkt *rtp.Packet) {
	if mc.h264Decoder == nil {
		return
	}

	au, err := mc.h264Decoder.Decode(pkt)
	if err != nil {
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Update SPS/PPS from in-band parameters
	spsChanged := false
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		typ := h264.NALUType(nalu[0] & 0x1F)
		switch typ {
		case h264.NALUTypeSPS:
			if !bytes.Equal(mc.videoSPS, nalu) {
				mc.videoSPS = nalu
				spsChanged = true
			}
		case h264.NALUTypePPS:
			mc.videoPPS = nalu
		}
	}

	// Generate or regenerate init segment
	if mc.initSegment == nil || spsChanged {
		initSeg, err := mc.buildInitSegment()
		if err != nil {
			return
		}
		mc.initSegment = initSeg
		mc.videoReady = true

		if spsChanged {
			mc.videoDTS = 0
			mc.audioDTS = 0
			mc.seqNum = 0
			mc.hasFirstVideo = false
		}

		// Push init segment to all connected clients so late-joiners
		// and SPS-change cases both work correctly.
		for _, c := range mc.clients {
			c.send(initSeg)
		}
	}

	if !mc.videoReady {
		return
	}

	// Wait for a keyframe before sending media segments
	if !mc.hasFirstVideo {
		if !h264.IsRandomAccess(au) {
			return
		}
		mc.lastVideoRTP = pkt.Timestamp
		mc.hasFirstVideo = true
	}

	var sampleDuration uint32
	rtpDelta := pkt.Timestamp - mc.lastVideoRTP
	if rtpDelta > 0 && rtpDelta < 90000*2 {
		sampleDuration = rtpDelta
	} else {
		sampleDuration = 90000 / 30
	}
	mc.lastVideoRTP = pkt.Timestamp

	sample := &fmp4.Sample{
		Duration: sampleDuration,
	}
	if err := sample.FillH264(0, au); err != nil {
		return
	}

	part := fmp4.Part{
		SequenceNumber: mc.seqNum,
		Tracks: []*fmp4.PartTrack{
			{
				ID:       1,
				BaseTime: mc.videoDTS,
				Samples:  []*fmp4.Sample{sample},
			},
		},
	}

	var buf seekableBuffer
	if err := part.Marshal(&buf); err != nil {
		return
	}

	mc.seqNum++
	mc.videoDTS += uint64(sampleDuration)

	data := buf.Bytes()
	for _, c := range mc.clients {
		c.send(data)
	}
}

func (mc *mseConsumer) OnAudioRTP(pkt *rtp.Packet) {
	if mc.aacDecoder == nil {
		return
	}

	aus, err := mc.aacDecoder.Decode(pkt)
	if err != nil {
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	if !mc.videoReady || !mc.hasFirstVideo {
		return
	}

	for _, au := range aus {
		sample := &fmp4.Sample{
			Duration: 1024,
			Payload:  au,
		}

		part := fmp4.Part{
			SequenceNumber: mc.seqNum,
			Tracks: []*fmp4.PartTrack{
				{
					ID:       2,
					BaseTime: mc.audioDTS,
					Samples:  []*fmp4.Sample{sample},
				},
			},
		}

		var buf seekableBuffer
		if err := part.Marshal(&buf); err != nil {
			continue
		}

		mc.seqNum++
		mc.audioDTS += 1024

		data := buf.Bytes()
		for _, c := range mc.clients {
			c.send(data)
		}
	}
}

func (mc *mseConsumer) OnDisconnect() {}

// addClient registers a client and returns the codec string (computed under lock).
// The init segment is sent immediately if available; otherwise the client
// receives it when the first keyframe arrives (broadcast in OnVideoRTP).
func (mc *mseConsumer) addClient(c *mseClient) string {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.initSegment != nil {
		c.send(mc.initSegment)
	}

	mc.clients = append(mc.clients, c)
	return mc.codecString()
}

func (mc *mseConsumer) removeClient(c *mseClient) int {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for i, existing := range mc.clients {
		if existing == c {
			mc.clients = append(mc.clients[:i], mc.clients[i+1:]...)
			break
		}
	}
	return len(mc.clients)
}

// MSEManager manages per-camera MSE consumers.
type MSEManager struct {
	hub            *rtsp.Hub
	allowedOrigins []string
	trustedProxies []netip.Prefix
	mu             sync.Mutex
	consumers      map[string]*mseConsumer
}

// NewMSEManager creates an MSE manager.
func NewMSEManager(hub *rtsp.Hub, allowedOrigins, trustedProxies []string) *MSEManager {
	return &MSEManager{
		hub:            hub,
		allowedOrigins: append([]string(nil), allowedOrigins...),
		trustedProxies: parseTrustedProxies(trustedProxies),
		consumers:      make(map[string]*mseConsumer),
	}
}

func parseTrustedProxies(values []string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil {
			result = append(result, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(value); err == nil {
			result = append(result, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return result
}

func remoteAddrMatchesTrustedProxy(remoteAddr string, trusted []netip.Prefix) bool {
	if len(trusted) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func originAllowed(r *http.Request, allowedOrigins []string, trustedProxies []netip.Prefix) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	normalized := u.Scheme + "://" + u.Host
	reqScheme := "http"
	if r.TLS != nil {
		reqScheme = "https"
	} else if remoteAddrMatchesTrustedProxy(r.RemoteAddr, trustedProxies) {
		if fp := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); fp != "" {
			reqScheme = strings.ToLower(fp)
		}
	}
	if strings.EqualFold(normalized, reqScheme+"://"+r.Host) {
		return true
	}
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), normalized) {
			return true
		}
	}
	return false
}

func (m *MSEManager) getOrCreateConsumer(rtspURL string) *mseConsumer {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.consumers[rtspURL]; ok {
		return c
	}

	source := m.hub.GetOrCreate(rtspURL)
	mc := newMSEConsumer(source.VideoTrack(), source.AudioTrack())
	m.consumers[rtspURL] = mc
	source.AddConsumer(mc)

	return mc
}

func (m *MSEManager) removeConsumerIfEmpty(rtspURL string, expected *mseConsumer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.consumers[rtspURL]
	if !ok || current != expected {
		// Another goroutine already replaced or removed this consumer
		return
	}

	// Double-check under manager lock that the consumer is truly empty.
	// A new client may have been added between removeClient and this call.
	expected.mu.Lock()
	count := len(expected.clients)
	expected.mu.Unlock()

	if count > 0 {
		return
	}

	source := m.hub.Get(rtspURL)
	if source != nil {
		source.RemoveConsumer(expected)
	}
	delete(m.consumers, rtspURL)
}

// HandleWebSocket upgrades an HTTP request to a WebSocket and streams fMP4 to the client.
func (m *MSEManager) HandleWebSocket(w http.ResponseWriter, r *http.Request, cameraName, rtspURL string) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(req *http.Request) bool {
			return originAllowed(req, m.allowedOrigins, m.trustedProxies)
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("MSE WebSocket upgrade failed", "camera", cameraName, "error", err)
		return
	}

	consumer := m.getOrCreateConsumer(rtspURL)
	client := newMSEClient(conn)

	// addClient computes the codec string under the consumer lock,
	// ensuring it's consistent with the current SPS/PPS and init segment.
	codecStr := consumer.addClient(client)

	// Send codec string before starting write pump — no concurrent writers.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(codecStr)); err != nil {
		slog.Error("MSE: failed to send codec string", "error", err)
		consumer.removeClient(client)
		conn.Close()
		return
	}

	slog.Info("MSE client connected", "camera", cameraName, "codec", codecStr)

	go client.writePump()

	// Read pump — drain incoming messages and enforce read deadline
	go func() {
		defer func() {
			client.close()
			remaining := consumer.removeClient(client)
			slog.Info("MSE client disconnected", "camera", cameraName, "remaining", remaining)

			if remaining == 0 {
				m.removeConsumerIfEmpty(rtspURL, consumer)
			}
		}()

		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(mseReadTimeout))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(mseReadTimeout))
			return nil
		})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()
}

// Close shuts down all MSE consumers and disconnects all clients.
func (m *MSEManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for url, consumer := range m.consumers {
		consumer.mu.Lock()
		for _, c := range consumer.clients {
			c.close()
		}
		consumer.clients = nil
		consumer.mu.Unlock()

		if m.hub != nil {
			if source := m.hub.Get(url); source != nil {
				source.RemoveConsumer(consumer)
			}
		}
	}
	m.consumers = make(map[string]*mseConsumer)
}
