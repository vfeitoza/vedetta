package stream

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// StreamManager manages per-camera WebRTC sessions and RTP forwarding.
type StreamManager struct {
	mu       sync.Mutex
	sessions map[string]*cameraSession
}

type cameraSession struct {
	mu    sync.Mutex
	peers []*peerState
	// RTP listener for this camera
	rtpPort  int
	rtpConn  *net.UDPConn
	stopCh   chan struct{}
	running  bool
	rtspURL  string
	ffmpegMu sync.Mutex
}

type peerState struct {
	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticRTP
}

func NewStreamManager() *StreamManager {
	return &StreamManager{
		sessions: make(map[string]*cameraSession),
	}
}

// getOrCreateSession returns the session for a camera, creating one if needed.
func (sm *StreamManager) getOrCreateSession(cameraName, rtspURL string) *cameraSession {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, ok := sm.sessions[cameraName]; ok {
		return s
	}

	s := &cameraSession{
		stopCh:  make(chan struct{}),
		rtspURL: rtspURL,
	}
	sm.sessions[cameraName] = s
	return s
}

// HandleOffer processes a WebRTC SDP offer and returns an SDP answer.
func (sm *StreamManager) HandleOffer(cameraName, rtspURL string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	session := sm.getOrCreateSession(cameraName, rtspURL)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}

	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		fmt.Sprintf("watchpost-%s", cameraName),
	)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("create video track: %w", err)
	}

	if _, err := pc.AddTrack(videoTrack); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("add track: %w", err)
	}

	peer := &peerState{pc: pc, track: videoTrack}

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		slog.Info("WebRTC ICE state changed", "camera", cameraName, "state", state.String())
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateClosed {
			session.removePeer(peer)
			_ = pc.Close()
		}
	})

	session.addPeer(peer)

	// Start RTP forwarding if not already running
	session.ffmpegMu.Lock()
	if !session.running {
		if err := session.startRTPForwarding(); err != nil {
			session.ffmpegMu.Unlock()
			session.removePeer(peer)
			_ = pc.Close()
			return nil, fmt.Errorf("start RTP forwarding: %w", err)
		}
	}
	session.ffmpegMu.Unlock()

	if err := pc.SetRemoteDescription(offer); err != nil {
		session.removePeer(peer)
		_ = pc.Close()
		return nil, fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		session.removePeer(peer)
		_ = pc.Close()
		return nil, fmt.Errorf("create answer: %w", err)
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		session.removePeer(peer)
		_ = pc.Close()
		return nil, fmt.Errorf("set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	return pc.LocalDescription(), nil
}

func (s *cameraSession) addPeer(peer *peerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers = append(s.peers, peer)
}

func (s *cameraSession) removePeer(peer *peerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.peers {
		if p == peer {
			s.peers = append(s.peers[:i], s.peers[i+1:]...)
			break
		}
	}

	// Stop RTP forwarding if no peers remain
	if len(s.peers) == 0 {
		s.stopForwarding()
	}
}

func (s *cameraSession) startRTPForwarding() error {
	port, err := findFreeUDPPort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	s.rtpPort = port

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("resolve UDP addr: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}
	s.rtpConn = conn
	s.running = true
	s.stopCh = make(chan struct{})

	// Start ffmpeg to transcode RTSP to RTP
	if _, err := StartRTPStream(s.rtspURL, port); err != nil {
		_ = conn.Close()
		s.running = false
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Forward RTP packets to all connected peers
	go s.forwardRTP()

	return nil
}

func (s *cameraSession) forwardRTP() {
	buf := make([]byte, 1500)
	packet := &rtp.Packet{}

	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		n, err := s.rtpConn.Read(buf)
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				slog.Error("RTP read error", "error", err)
				continue
			}
		}

		if err := packet.Unmarshal(buf[:n]); err != nil {
			continue
		}

		s.mu.Lock()
		for _, peer := range s.peers {
			if err := peer.track.WriteRTP(packet); err != nil {
				slog.Debug("failed to write RTP to peer", "error", err)
			}
		}
		s.mu.Unlock()
	}
}

func (s *cameraSession) stopForwarding() {
	s.ffmpegMu.Lock()
	defer s.ffmpegMu.Unlock()

	if !s.running {
		return
	}
	close(s.stopCh)
	if s.rtpConn != nil {
		_ = s.rtpConn.Close()
	}
	s.running = false
}

func findFreeUDPPort() (int, error) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return 0, err
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	return port, nil
}

// Close shuts down all sessions and peer connections.
func (sm *StreamManager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, session := range sm.sessions {
		session.mu.Lock()
		for _, peer := range session.peers {
			_ = peer.pc.Close()
		}
		session.peers = nil
		session.mu.Unlock()
		session.stopForwarding()
	}
	sm.sessions = make(map[string]*cameraSession)
}
