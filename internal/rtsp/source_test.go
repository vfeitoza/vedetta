package rtsp

import (
	"sync"
	"testing"

	"github.com/pion/rtp"
)

// mockConsumer records calls to OnVideoRTP, OnAudioRTP, OnDisconnect.
type mockConsumer struct {
	mu            sync.Mutex
	videoPkts     int
	audioPkts     int
	disconnects   int
	lastVideoPkt  *rtp.Packet
}

func (m *mockConsumer) OnVideoRTP(pkt *rtp.Packet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoPkts++
	m.lastVideoPkt = pkt
}

func (m *mockConsumer) OnAudioRTP(_ *rtp.Packet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioPkts++
}

func (m *mockConsumer) OnDisconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnects++
}

func TestSourceAddRemoveConsumer(t *testing.T) {
	s := NewSource("rtsp://test:554/stream")

	c1 := &mockConsumer{}
	c2 := &mockConsumer{}

	s.AddConsumer(c1)
	s.AddConsumer(c2)

	if s.ConsumerCount() != 2 {
		t.Fatalf("expected 2 consumers, got %d", s.ConsumerCount())
	}

	s.RemoveConsumer(c1)

	if s.ConsumerCount() != 1 {
		t.Fatalf("expected 1 consumer after remove, got %d", s.ConsumerCount())
	}

	s.RemoveConsumer(c2)

	if s.ConsumerCount() != 0 {
		t.Fatalf("expected 0 consumers after remove, got %d", s.ConsumerCount())
	}
}

func TestSourceRemoveNonexistent(t *testing.T) {
	s := NewSource("rtsp://test:554/stream")
	c1 := &mockConsumer{}
	c2 := &mockConsumer{}

	s.AddConsumer(c1)
	s.RemoveConsumer(c2) // should not panic

	if s.ConsumerCount() != 1 {
		t.Fatalf("expected 1 consumer, got %d", s.ConsumerCount())
	}
}

func TestSourceFanOutVideo(t *testing.T) {
	s := NewSource("rtsp://test:554/stream")

	c1 := &mockConsumer{}
	c2 := &mockConsumer{}
	s.AddConsumer(c1)
	s.AddConsumer(c2)

	pkt := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 42},
		Payload: []byte{1, 2, 3},
	}

	s.fanOutVideo(pkt)

	c1.mu.Lock()
	defer c1.mu.Unlock()
	c2.mu.Lock()
	defer c2.mu.Unlock()

	if c1.videoPkts != 1 {
		t.Errorf("c1 got %d video pkts, want 1", c1.videoPkts)
	}
	if c2.videoPkts != 1 {
		t.Errorf("c2 got %d video pkts, want 1", c2.videoPkts)
	}
	if c1.lastVideoPkt.SequenceNumber != 42 {
		t.Errorf("c1 got seq %d, want 42", c1.lastVideoPkt.SequenceNumber)
	}
}

func TestSourceFanOutAudio(t *testing.T) {
	s := NewSource("rtsp://test:554/stream")

	c := &mockConsumer{}
	s.AddConsumer(c)

	s.fanOutAudio(&rtp.Packet{Payload: []byte{0xAA}})

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.audioPkts != 1 {
		t.Errorf("got %d audio pkts, want 1", c.audioPkts)
	}
}

func TestSourceNotifyDisconnect(t *testing.T) {
	s := NewSource("rtsp://test:554/stream")

	c1 := &mockConsumer{}
	c2 := &mockConsumer{}
	s.AddConsumer(c1)
	s.AddConsumer(c2)

	s.notifyDisconnect()

	c1.mu.Lock()
	defer c1.mu.Unlock()
	c2.mu.Lock()
	defer c2.mu.Unlock()

	if c1.disconnects != 1 {
		t.Errorf("c1 got %d disconnects, want 1", c1.disconnects)
	}
	if c2.disconnects != 1 {
		t.Errorf("c2 got %d disconnects, want 1", c2.disconnects)
	}

	if s.Connected() {
		t.Error("source should be disconnected after notifyDisconnect")
	}
}

func TestSourceURL(t *testing.T) {
	url := "rtsp://test:554/stream"
	s := NewSource(url)
	if s.URL() != url {
		t.Errorf("URL() = %q, want %q", s.URL(), url)
	}
}

func TestSourceTrackInfo_NilBeforeConnect(t *testing.T) {
	s := NewSource("rtsp://test:554/stream")
	if s.VideoTrack() != nil {
		t.Error("VideoTrack should be nil before connect")
	}
	if s.AudioTrack() != nil {
		t.Error("AudioTrack should be nil before connect")
	}
	if s.Connected() {
		t.Error("Connected should be false before connect")
	}
}
