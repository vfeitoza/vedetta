package stream

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/rvben/vedetta/internal/rtsp"
)

// StreamManager manages per-camera WebRTC sessions with direct RTP forwarding.
type StreamManager struct {
	hub *rtsp.Hub
	mu  sync.Mutex
	// One webrtcConsumer per camera URL, shared across all peers watching that camera.
	consumers map[string]*webrtcConsumer
}

// rtpWriter is the minimal interface needed to forward a packet onto a peer's
// outbound track. Pion's *webrtc.TrackLocalStaticRTP satisfies it; tests use a
// recording fake.
type rtpWriter interface {
	WriteRTP(pkt *rtp.Packet) error
}

// trackState handles per-track sequence/timestamp rewriting for mid-stream
// joins. The outbound sequence number is allocated monotonically by the track,
// independent of the source RTP sequence, so a single inbound packet may
// produce multiple outbound packets (e.g. FU-A fragments) without collisions.
type trackState struct {
	track rtpWriter

	mu       sync.Mutex
	outSeq   uint16
	tsOffset uint32
	started  bool
}

// write forwards pkt with a freshly assigned outbound sequence number and the
// timestamp rebased to start near zero. The caller's pkt is not mutated.
func (t *trackState) write(pkt *rtp.Packet) error {
	t.mu.Lock()
	if !t.started {
		t.tsOffset = -pkt.Timestamp
		t.started = true
	}
	seq := t.outSeq
	t.outSeq++
	ts := pkt.Timestamp + t.tsOffset
	t.mu.Unlock()

	clone := *pkt
	clone.SequenceNumber = seq
	clone.Timestamp = ts
	return t.track.WriteRTP(&clone)
}

type peerState struct {
	pc         *webrtc.PeerConnection
	cameraName string
	video      *trackState
	audio      *trackState // nil if camera has no supported audio

	// Parameter sets cached from the RTSP SDP. When the first forwarded
	// keyframe is a bare IDR (NAL type 5) — common for cameras that only
	// advertise SPS/PPS out-of-band — we inject these so the browser
	// decoder can bootstrap. Without them, the decoder waits forever and
	// the <video> element stays at readyState=0 with framesReceived=0.
	sps []byte
	pps []byte

	mu           sync.Mutex
	keyframeSeen bool

	// Forwarding diagnostics. WriteRTP can fail silently deep in the pion
	// stack (e.g. a buffer pool with a fixed-size slot rejects payloads
	// larger than its cap with io.ErrShortBuffer). Without counters we
	// can't tell a healthy stream from a stream where 95% of packets are
	// being dropped — the symptoms are identical to upstream packet loss.
	videoCalls    atomic.Uint64
	videoOK       atomic.Uint64
	videoErr      atomic.Uint64
	videoBytes    atomic.Uint64
	videoMaxSize  atomic.Uint64
	videoOver1200 atomic.Uint64
	videoOver1400 atomic.Uint64
	// Inbound NAL type classification — reveals whether the camera is
	// sending huge single-NAL packets (fragmentable) or huge STAP-A
	// aggregates (not currently fragmented and likely the reason
	// keyframes don't assemble in the browser).
	inSingleIDR atomic.Uint64
	inSingleP   atomic.Uint64
	inSingleSPS atomic.Uint64
	inSinglePPS atomic.Uint64
	inSTAPA     atomic.Uint64
	inFUA       atomic.Uint64
	inOther     atomic.Uint64
	// Outbound packet counter — every WriteRTP call to the track, after
	// SPS/PPS injection and FU-A fragmentation expand each inbound packet
	// into one or more outbound packets.
	outPkts       atomic.Uint64
	loggedFirstKF atomic.Bool
	audioCalls    atomic.Uint64
	audioOK       atomic.Uint64
	audioErr      atomic.Uint64
	loggedErr     atomic.Bool
	statsCancel   context.CancelFunc
}

// classifyInbound bumps the NAL-type counter for pkt. The classification
// matches isKeyframe's NAL-type cases but covers every type so the periodic
// stats line shows the full inbound composition.
func (p *peerState) classifyInbound(pkt *rtp.Packet) {
	if len(pkt.Payload) < 1 {
		p.inOther.Add(1)
		return
	}
	switch nt := pkt.Payload[0] & 0x1f; {
	case nt == 5:
		p.inSingleIDR.Add(1)
	case nt == 1:
		p.inSingleP.Add(1)
	case nt == 7:
		p.inSingleSPS.Add(1)
	case nt == 8:
		p.inSinglePPS.Add(1)
	case nt == 24:
		p.inSTAPA.Add(1)
	case nt == 28:
		p.inFUA.Add(1)
	default:
		p.inOther.Add(1)
	}
}

func (p *peerState) writeVideo(pkt *rtp.Packet) error {
	p.mu.Lock()
	needsParams := false
	if !p.keyframeSeen {
		if !isKeyframe(pkt) {
			p.mu.Unlock()
			return nil
		}
		p.keyframeSeen = true
		needsParams = len(p.sps) > 0 && len(p.pps) > 0 && !containsParameterSets(pkt)
	}
	p.mu.Unlock()

	p.videoCalls.Add(1)
	p.classifyInbound(pkt)
	sz := uint64(len(pkt.Payload))
	for {
		cur := p.videoMaxSize.Load()
		if sz <= cur || p.videoMaxSize.CompareAndSwap(cur, sz) {
			break
		}
	}
	if sz > 1400 {
		p.videoOver1400.Add(1)
	}
	if sz > 1200 {
		p.videoOver1200.Add(1)
	}

	var nalType byte
	if len(pkt.Payload) > 0 {
		nalType = pkt.Payload[0] & 0x1f
	}

	if needsParams {
		if err := p.video.write(buildNALPacket(pkt, p.sps)); err != nil {
			p.recordWriteErr("video", err, pkt)
			return err
		}
		p.outPkts.Add(1)
		if err := p.video.write(buildNALPacket(pkt, p.pps)); err != nil {
			p.recordWriteErr("video", err, pkt)
			return err
		}
		p.outPkts.Add(1)
	}

	fragments := 0
	var frags []*rtp.Packet
	if f := fragmentSingleNAL(pkt, fuaMTU); f != nil {
		frags = f
	} else if f := refragmentFUA(pkt, fuaMTU); f != nil {
		frags = f
	}
	if frags != nil {
		fragments = len(frags)
		for _, frag := range frags {
			if err := p.video.write(frag); err != nil {
				p.recordWriteErr("video", err, pkt)
				return err
			}
			p.outPkts.Add(1)
		}
	} else {
		if err := p.video.write(pkt); err != nil {
			p.recordWriteErr("video", err, pkt)
			return err
		}
		p.outPkts.Add(1)
	}

	if p.loggedFirstKF.CompareAndSwap(false, true) {
		slog.Info("WebRTC first keyframe forwarded",
			"camera", p.cameraName,
			"inNAL", nalType,
			"inSize", sz,
			"spsLen", len(p.sps),
			"ppsLen", len(p.pps),
			"injectedParams", needsParams,
			"fragments", fragments,
			"sps", hex.EncodeToString(p.sps),
			"pps", hex.EncodeToString(p.pps),
		)
	}

	p.videoOK.Add(1)
	p.videoBytes.Add(sz)
	return nil
}

func (p *peerState) recordWriteErr(kind string, err error, pkt *rtp.Packet) {
	if kind == "video" {
		p.videoErr.Add(1)
	} else {
		p.audioErr.Add(1)
	}
	if p.loggedErr.CompareAndSwap(false, true) {
		slog.Warn("WebRTC write error",
			"camera", p.cameraName,
			"kind", kind,
			"error", err.Error(),
			"errType", fmt.Sprintf("%T", err),
			"payloadLen", len(pkt.Payload),
			"seq", pkt.SequenceNumber,
		)
	}
}

func (p *peerState) startStatsLogger(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	p.statsCancel = cancel
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		start := time.Now()
		var lastV, lastVOK, lastA uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				v := p.videoCalls.Load()
				vok := p.videoOK.Load()
				verr := p.videoErr.Load()
				a := p.audioCalls.Load()
				slog.Info("WebRTC peer stats",
					"camera", p.cameraName,
					"t", int(time.Since(start).Seconds()),
					"vCalls", v,
					"vOK", vok,
					"vErr", verr,
					"vBytes", p.videoBytes.Load(),
					"vMax", p.videoMaxSize.Load(),
					"vOver1200", p.videoOver1200.Load(),
					"vOver1400", p.videoOver1400.Load(),
					"vRate", v-lastV,
					"vOKRate", vok-lastVOK,
					"outPkts", p.outPkts.Load(),
					"inIDR", p.inSingleIDR.Load(),
					"inP", p.inSingleP.Load(),
					"inSPS", p.inSingleSPS.Load(),
					"inPPS", p.inSinglePPS.Load(),
					"inSTAPA", p.inSTAPA.Load(),
					"inFUA", p.inFUA.Load(),
					"inOther", p.inOther.Load(),
					"aCalls", a,
					"aRate", a-lastA,
				)
				lastV, lastVOK, lastA = v, vok, a
			}
		}
	}()
}

// containsParameterSets reports whether pkt already carries an SPS (NAL 7) or
// PPS (NAL 8), either as a single NAL or aggregated inside a STAP-A. When true,
// injecting cached parameter sets would just produce duplicates.
func containsParameterSets(pkt *rtp.Packet) bool {
	if len(pkt.Payload) < 1 {
		return false
	}
	nalType := pkt.Payload[0] & 0x1f
	switch nalType {
	case 7, 8:
		return true
	case 24: // STAP-A: scan every aggregated NAL header
		offset := 1
		for offset+2 <= len(pkt.Payload) {
			size := int(pkt.Payload[offset])<<8 | int(pkt.Payload[offset+1])
			offset += 2
			if size < 1 || offset+size > len(pkt.Payload) {
				return false
			}
			if inner := pkt.Payload[offset] & 0x1f; inner == 7 || inner == 8 {
				return true
			}
			offset += size
		}
	}
	return false
}

// buildNALPacket synthesizes a single-NAL RTP packet that piggy-backs on
// template's SSRC, payload type, and timestamp so it lands in the same access
// unit as the IDR that follows. The outbound sequence number is assigned by
// trackState.write, so callers must not rely on the SequenceNumber field here.
func buildNALPacket(template *rtp.Packet, nal []byte) *rtp.Packet {
	return &rtp.Packet{
		Header: rtp.Header{
			Version:     2,
			PayloadType: template.PayloadType,
			Timestamp:   template.Timestamp,
			SSRC:        template.SSRC,
		},
		Payload: append([]byte(nil), nal...),
	}
}

// fuaMTU is the maximum payload size for fragmented NAL units. Browsers
// commonly use 1200 as the effective WebRTC RTP MTU; values below that leave
// safe headroom for SRTP auth tags and any extension headers.
const fuaMTU = 1200

// fragmentSingleNAL splits a single NAL unit larger than mtu into FU-A
// fragments. Each fragment becomes its own RTP packet with the source's
// timestamp/SSRC/PayloadType and a FU-A indicator + header carrying a slice of
// the NAL data. Returns nil if the payload is not a single NAL or fits within
// mtu — callers should pass the original packet through unchanged in that case.
func fragmentSingleNAL(pkt *rtp.Packet, mtu int) []*rtp.Packet {
	payload := pkt.Payload
	if len(payload) <= mtu {
		return nil
	}
	if len(payload) < 1 {
		return nil
	}
	nalHeader := payload[0]
	nalType := nalHeader & 0x1f
	// Only fragment single NAL units (types 1–23). STAP-A (24) and FU-A (28)
	// have their own framing and must be passed through unchanged.
	if nalType < 1 || nalType > 23 {
		return nil
	}

	nalData := payload[1:]
	nri := nalHeader & 0x60
	fuIndicator := nri | 28 // type 28 = FU-A

	const fuHeaderLen = 2 // 1 byte FU indicator + 1 byte FU header
	fragSize := mtu - fuHeaderLen
	if fragSize <= 0 {
		return nil
	}

	fragments := make([]*rtp.Packet, 0, (len(nalData)+fragSize-1)/fragSize)
	offset := 0
	for offset < len(nalData) {
		end := offset + fragSize
		isLast := false
		if end >= len(nalData) {
			end = len(nalData)
			isLast = true
		}
		fuHeader := nalType
		if offset == 0 {
			fuHeader |= 0x80 // start bit
		}
		if isLast {
			fuHeader |= 0x40 // end bit
		}
		fragPayload := make([]byte, fuHeaderLen+(end-offset))
		fragPayload[0] = fuIndicator
		fragPayload[1] = fuHeader
		copy(fragPayload[fuHeaderLen:], nalData[offset:end])

		marker := false
		if isLast {
			marker = pkt.Marker
		}
		fragments = append(fragments, &rtp.Packet{
			Header: rtp.Header{
				Version:     2,
				PayloadType: pkt.PayloadType,
				Timestamp:   pkt.Timestamp,
				SSRC:        pkt.SSRC,
				Marker:      marker,
			},
			Payload: fragPayload,
		})
		offset = end
	}
	return fragments
}

// refragmentFUA re-splits an oversized FU-A packet into smaller FU-A pieces
// while preserving the original Start/End bit semantics. Returns nil when pkt
// is not a FU-A or already fits within mtu so the caller forwards it unchanged.
//
// Some cameras (e.g. Tapo C200 sub-stream) emit FU-A fragments at the camera's
// own MTU — well above the ~1500-byte slots Pion's receive-side packetio buffer
// allocates per packet. Forwarding those oversized FU-A packets through to the
// browser causes Pion to reject them with io.ErrShortBuffer, and the receiver
// silently loses every IDR fragment while smaller P-slice fragments slip
// through. The browser's H.264 decoder then has nothing to bootstrap from and
// the <video> element sits at framesDecoded=0 indefinitely.
func refragmentFUA(pkt *rtp.Packet, mtu int) []*rtp.Packet {
	payload := pkt.Payload
	if len(payload) < 2 || len(payload) <= mtu {
		return nil
	}
	fuIndicator := payload[0]
	if fuIndicator&0x1f != 28 {
		return nil
	}
	fuHeader := payload[1]
	innerNAL := fuHeader & 0x1f
	origStart := fuHeader & 0x80
	origEnd := fuHeader & 0x40

	nalData := payload[2:]
	const fuHeaderLen = 2
	fragSize := mtu - fuHeaderLen
	if fragSize <= 0 {
		return nil
	}

	fragments := make([]*rtp.Packet, 0, (len(nalData)+fragSize-1)/fragSize)
	offset := 0
	for offset < len(nalData) {
		end := offset + fragSize
		isLast := false
		if end >= len(nalData) {
			end = len(nalData)
			isLast = true
		}
		// Only the first emitted piece may carry the original Start bit;
		// only the last emitted piece may carry the original End bit. Middle
		// pieces are always S=0 E=0. This preserves correct reassembly at
		// the receiver regardless of whether the original FU-A was a head,
		// middle, tail, or (rare) single S+E fragment.
		newFuHeader := innerNAL
		if offset == 0 {
			newFuHeader |= origStart
		}
		if isLast {
			newFuHeader |= origEnd
		}
		fragPayload := make([]byte, fuHeaderLen+(end-offset))
		fragPayload[0] = fuIndicator
		fragPayload[1] = newFuHeader
		copy(fragPayload[fuHeaderLen:], nalData[offset:end])

		marker := false
		if isLast {
			marker = pkt.Marker
		}
		fragments = append(fragments, &rtp.Packet{
			Header: rtp.Header{
				Version:     2,
				PayloadType: pkt.PayloadType,
				Timestamp:   pkt.Timestamp,
				SSRC:        pkt.SSRC,
				Marker:      marker,
			},
			Payload: fragPayload,
		})
		offset = end
	}
	return fragments
}

func (p *peerState) writeAudio(pkt *rtp.Packet) error {
	if p.audio == nil {
		return nil
	}
	// Only forward audio after the first video keyframe
	p.mu.Lock()
	if !p.keyframeSeen {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	p.audioCalls.Add(1)
	if err := p.audio.write(pkt); err != nil {
		p.recordWriteErr("audio", err, pkt)
		return err
	}
	p.audioOK.Add(1)
	return nil
}

// isKeyframe checks if an RTP packet contains the start of an H264 IDR frame.
func isKeyframe(pkt *rtp.Packet) bool {
	if len(pkt.Payload) < 2 {
		return false
	}

	nalType := pkt.Payload[0] & 0x1f

	switch {
	case nalType >= 1 && nalType <= 23:
		// Single NAL unit: type 5 = IDR, type 7 = SPS
		return nalType == 5 || nalType == 7
	case nalType == 24:
		// STAP-A: check first NAL inside
		if len(pkt.Payload) < 4 {
			return false
		}
		innerNALType := pkt.Payload[3] & 0x1f
		return innerNALType == 5 || innerNALType == 7
	case nalType == 28:
		// FU-A: check start bit and NAL type
		startBit := pkt.Payload[1] & 0x80
		fuNALType := pkt.Payload[1] & 0x1f
		return startBit != 0 && (fuNALType == 5 || fuNALType == 7)
	}

	return false
}

// webrtcConsumer implements rtsp.Consumer and forwards RTP to WebRTC peers.
type webrtcConsumer struct {
	mu    sync.RWMutex
	peers []*peerState
}

func (wc *webrtcConsumer) OnVideoRTP(pkt *rtp.Packet) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	for _, p := range wc.peers {
		if err := p.writeVideo(pkt); err != nil {
			slog.Debug("failed to write video RTP to peer", "error", err)
		}
	}
}

func (wc *webrtcConsumer) OnAudioRTP(pkt *rtp.Packet) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	for _, p := range wc.peers {
		if err := p.writeAudio(pkt); err != nil {
			slog.Debug("failed to write audio RTP to peer", "error", err)
		}
	}
}

func (wc *webrtcConsumer) OnDisconnect() {}

func (wc *webrtcConsumer) addPeer(peer *peerState) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	wc.peers = append(wc.peers, peer)
}

func (wc *webrtcConsumer) removePeer(peer *peerState) int {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	for i, p := range wc.peers {
		if p == peer {
			wc.peers = append(wc.peers[:i], wc.peers[i+1:]...)
			break
		}
	}
	return len(wc.peers)
}

// NewStreamManager creates a stream manager that uses an RTSP Hub for direct forwarding.
func NewStreamManager(hub *rtsp.Hub) *StreamManager {
	return &StreamManager{
		hub:       hub,
		consumers: make(map[string]*webrtcConsumer),
	}
}

// audioCodecForTrack returns the WebRTC codec parameters for a camera's audio track.
// Only G.711 codecs (PCMU/PCMA) are supported for WebRTC passthrough.
// AAC cameras get audio via MSE streaming instead.
func audioCodecForTrack(at *rtsp.TrackInfo) *webrtc.RTPCodecParameters {
	if at == nil {
		return nil
	}
	switch at.Codec {
	case "PCMU":
		return &webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypePCMU,
				ClockRate: 8000,
				Channels:  1,
			},
			PayloadType: 0,
		}
	case "PCMA":
		return &webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypePCMA,
				ClockRate: 8000,
				Channels:  1,
			},
			PayloadType: 8,
		}
	default:
		slog.Info("audio codec not supported for WebRTC passthrough, use MSE for audio", "codec", at.Codec)
		return nil
	}
}

// HandleOffer processes a WebRTC SDP offer and returns an SDP answer.
func (sm *StreamManager) HandleOffer(cameraName, rtspURL string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	// Build H264 codec capability with profile-level-id from camera SPS
	sdpFmtpLine := "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"
	source := sm.hub.GetOrCreate(rtspURL)
	var spsForLog, ppsForLog []byte
	if vt := source.VideoTrack(); vt != nil && len(vt.SPS) >= 4 {
		profileLevelID := hex.EncodeToString(vt.SPS[1:4])
		sdpFmtpLine = fmt.Sprintf("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=%s", profileLevelID)
		spsForLog, ppsForLog = vt.SPS, vt.PPS
	}
	slog.Info("WebRTC offer received",
		"camera", cameraName,
		"sdpFmtpLine", sdpFmtpLine,
		"spsLen", len(spsForLog),
		"sps", hex.EncodeToString(spsForLog),
		"ppsLen", len(ppsForLog),
		"pps", hex.EncodeToString(ppsForLog),
	)

	// Register only the codecs we'll actually send.
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: sdpFmtpLine,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, fmt.Errorf("register video codec: %w", err)
	}

	// Register audio codec if the camera provides G.711
	audioCodec := audioCodecForTrack(source.AudioTrack())
	if audioCodec != nil {
		if err := me.RegisterCodec(*audioCodec, webrtc.RTPCodecTypeAudio); err != nil {
			slog.Warn("failed to register audio codec, continuing without audio", "error", err)
			audioCodec = nil
		}
	}

	// Force IPv4 only — IPv6 UDP causes packet loss on some networks
	se := webrtc.SettingEngine{}
	se.SetIPFilter(func(ip net.IP) bool {
		return ip.To4() != nil
	})
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})

	// Disable default interceptors. Pion's NACK responder buffers each outbound
	// RTP packet in a 1460-byte pool slot (interceptor/internal/rtpbuffer),
	// rejecting any packet with payload larger than 1460 bytes with
	// io.ErrShortBuffer. Cameras commonly emit RTP packets up to ~1460–1500
	// bytes, so leaving this on silently drops the majority of video — only
	// fragments small enough to fit slip through. We're a one-way forwarder:
	// the camera can't honor NACK retransmission requests, so caching outbound
	// packets buys us nothing. TWCC and stats interceptors are likewise dead
	// weight for our use case.
	ir := &interceptor.Registry{}
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithSettingEngine(se),
		webrtc.WithInterceptorRegistry(ir),
	)
	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}

	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: sdpFmtpLine,
		},
		"video",
		fmt.Sprintf("vedetta-%s", cameraName),
	)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("create video track: %w", err)
	}

	if _, err := pc.AddTrack(videoTrack); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("add video track: %w", err)
	}

	peer := &peerState{
		pc:         pc,
		cameraName: cameraName,
		video:      &trackState{track: videoTrack},
	}
	peer.startStatsLogger(context.Background())
	if vt := source.VideoTrack(); vt != nil {
		if len(vt.SPS) > 0 {
			peer.sps = append([]byte(nil), vt.SPS...)
		}
		if len(vt.PPS) > 0 {
			peer.pps = append([]byte(nil), vt.PPS...)
		}
	}

	// Add audio track if G.711
	if audioCodec != nil {
		audioTrack, err := webrtc.NewTrackLocalStaticRTP(
			audioCodec.RTPCodecCapability,
			"audio",
			fmt.Sprintf("vedetta-%s-audio", cameraName),
		)
		if err != nil {
			slog.Warn("failed to create audio track, continuing without audio", "error", err)
		} else if _, err := pc.AddTrack(audioTrack); err != nil {
			slog.Warn("failed to add audio track, continuing without audio", "error", err)
		} else {
			peer.audio = &trackState{track: audioTrack}
			slog.Info("WebRTC audio enabled", "camera", cameraName, "codec", source.AudioTrack().Codec)
		}
	}

	if sm.hub == nil {
		_ = pc.Close()
		return nil, fmt.Errorf("no RTSP hub configured")
	}

	// Get or create the consumer for this RTSP URL
	consumer := sm.getOrCreateConsumer(rtspURL)
	consumer.addPeer(peer)

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		slog.Info("WebRTC ICE state changed", "camera", cameraName, "state", state.String())
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateClosed {
			if peer.statsCancel != nil {
				peer.statsCancel()
			}
			remaining := consumer.removePeer(peer)
			_ = pc.Close()

			// Remove consumer from Hub if no peers remain
			if remaining == 0 {
				sm.mu.Lock()
				source := sm.hub.Get(rtspURL)
				if source != nil {
					source.RemoveConsumer(consumer)
				}
				delete(sm.consumers, rtspURL)
				sm.mu.Unlock()
			}
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		consumer.removePeer(peer)
		_ = pc.Close()
		return nil, fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		consumer.removePeer(peer)
		_ = pc.Close()
		return nil, fmt.Errorf("create answer: %w", err)
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		consumer.removePeer(peer)
		_ = pc.Close()
		return nil, fmt.Errorf("set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	return pc.LocalDescription(), nil
}

func (sm *StreamManager) getOrCreateConsumer(rtspURL string) *webrtcConsumer {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if c, ok := sm.consumers[rtspURL]; ok {
		return c
	}

	c := &webrtcConsumer{}
	sm.consumers[rtspURL] = c

	// Register with the Hub's source
	source := sm.hub.GetOrCreate(rtspURL)
	source.AddConsumer(c)

	return c
}

// Close shuts down all sessions and peer connections.
func (sm *StreamManager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for url, consumer := range sm.consumers {
		consumer.mu.Lock()
		for _, peer := range consumer.peers {
			_ = peer.pc.Close()
		}
		consumer.peers = nil
		consumer.mu.Unlock()

		if sm.hub != nil {
			if source := sm.hub.Get(url); source != nil {
				source.RemoveConsumer(consumer)
			}
		}
	}
	sm.consumers = make(map[string]*webrtcConsumer)
}
