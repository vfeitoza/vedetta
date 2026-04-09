package stream

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"sync"

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

// trackState handles per-track sequence/timestamp rewriting for mid-stream joins.
type trackState struct {
	track *webrtc.TrackLocalStaticRTP

	mu        sync.Mutex
	seqOffset uint16
	tsOffset  uint32
	started   bool
}

func (t *trackState) write(pkt *rtp.Packet) error {
	t.mu.Lock()
	if !t.started {
		t.seqOffset = -pkt.SequenceNumber
		t.tsOffset = -pkt.Timestamp
		t.started = true
	}
	seq := pkt.SequenceNumber + t.seqOffset
	ts := pkt.Timestamp + t.tsOffset
	t.mu.Unlock()

	clone := *pkt
	clone.SequenceNumber = seq
	clone.Timestamp = ts
	return t.track.WriteRTP(&clone)
}

type peerState struct {
	pc    *webrtc.PeerConnection
	video *trackState
	audio *trackState // nil if camera has no supported audio

	mu           sync.Mutex
	keyframeSeen bool
}

func (p *peerState) writeVideo(pkt *rtp.Packet) error {
	p.mu.Lock()
	if !p.keyframeSeen {
		if isKeyframe(pkt) {
			p.keyframeSeen = true
		} else {
			p.mu.Unlock()
			return nil
		}
	}
	p.mu.Unlock()

	return p.video.write(pkt)
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

	return p.audio.write(pkt)
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
	if vt := source.VideoTrack(); vt != nil && len(vt.SPS) >= 3 {
		profileLevelID := hex.EncodeToString(vt.SPS[1:4])
		sdpFmtpLine = fmt.Sprintf("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=%s", profileLevelID)
	}

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

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se))
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
		pc:    pc,
		video: &trackState{track: videoTrack},
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
