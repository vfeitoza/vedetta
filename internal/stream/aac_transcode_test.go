package stream

import (
	"errors"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/rtsp"
)

// fakeAACEncoder stands in for the libfdk-aac-backed encoder so the
// transcode wiring can be tested without the native library present. It
// emits one opaque "AAC frame" per 1024 input PCM samples, matching the
// AAC-LC frame size the real encoder produces.
type fakeAACEncoder struct {
	closed bool
	fed    int
}

func (f *fakeAACEncoder) Encode(pcm []int16) ([][]byte, error) {
	f.fed += len(pcm)
	var out [][]byte
	for len(pcm) >= 1024 {
		out = append(out, []byte{0xDE, 0xAD, 0xBE, 0xEF})
		pcm = pcm[1024:]
	}
	return out, nil
}

func (f *fakeAACEncoder) Close() { f.closed = true }

func withFakeEncoder(t *testing.T, factory func(rate, channels int) (aacEncoder, error)) {
	t.Helper()
	orig := newAACEncoder
	newAACEncoder = factory
	t.Cleanup(func() { newAACEncoder = orig })
}

func TestHLSTranscodesG711ToAAC(t *testing.T) {
	var enc *fakeAACEncoder
	withFakeEncoder(t, func(rate, channels int) (aacEncoder, error) {
		if rate != 8000 || channels != 1 {
			t.Fatalf("encoder requested with rate=%d channels=%d, want 8000/1", rate, channels)
		}
		enc = &fakeAACEncoder{}
		return enc, nil
	})

	audio := &rtsp.TrackInfo{Codec: "PCMU", ClockRate: 8000, ChannelCount: 1}
	c := newHLSConsumer(&rtsp.TrackInfo{Codec: "H264"}, audio)

	if !c.hasAudio {
		t.Fatal("G.711 audio with an available AAC encoder must enable HLS audio")
	}
	if c.aacConfig == nil || c.aacConfig.Type != mpeg4audio.ObjectTypeAACLC {
		t.Fatalf("init must advertise AAC-LC, got %+v", c.aacConfig)
	}
	if c.audioTimeScale != 8000 {
		t.Fatalf("audio timescale = %d, want 8000 (G.711 source rate)", c.audioTimeScale)
	}

	// Drive the transcode path: one RTP packet carrying 2048 µ-law bytes
	// (2048 samples) must yield two 1024-sample AAC frames.
	c.videoReady = true
	c.hasFirstKeyframe = true
	c.OnAudioRTP(&rtp.Packet{Payload: make([]byte, 2048)})

	if enc.fed != 2048 {
		t.Fatalf("encoder fed %d PCM samples, want 2048 (decoded from 2048 G.711 bytes)", enc.fed)
	}
	if len(c.segAudio) != 2 {
		t.Fatalf("expected 2 transcoded AAC samples in the segment, got %d", len(c.segAudio))
	}
	if c.segAudio[0].Duration != hlsAudioSampleDuration {
		t.Fatalf("AAC sample duration = %d, want %d", c.segAudio[0].Duration, hlsAudioSampleDuration)
	}
}

func TestHLSG711WithoutEncoderIsVideoOnly(t *testing.T) {
	withFakeEncoder(t, func(rate, channels int) (aacEncoder, error) {
		return nil, errors.New("libfdk-aac not available")
	})

	c := newHLSConsumer(
		&rtsp.TrackInfo{Codec: "H264"},
		&rtsp.TrackInfo{Codec: "PCMA", ClockRate: 8000, ChannelCount: 1},
	)

	if c.hasAudio {
		t.Fatal("without an AAC encoder, G.711 audio must be dropped (video-only), not enabled")
	}

	// Feeding audio must be a harmless no-op, never a panic.
	c.videoReady = true
	c.hasFirstKeyframe = true
	c.OnAudioRTP(&rtp.Packet{Payload: make([]byte, 1024)})
	if len(c.segAudio) != 0 {
		t.Fatalf("video-only path must not accumulate audio samples, got %d", len(c.segAudio))
	}
}

func TestHLSNativeAACStillPassthrough(t *testing.T) {
	// A real AAC camera must keep the zero-transcode path: the G.711
	// encoder factory must never be consulted.
	withFakeEncoder(t, func(rate, channels int) (aacEncoder, error) {
		t.Fatal("AAC source must not invoke the G.711->AAC encoder")
		return nil, nil
	})

	c := newHLSConsumer(
		&rtsp.TrackInfo{Codec: "H264"},
		&rtsp.TrackInfo{Codec: "AAC", ClockRate: 16000, ChannelCount: 1},
	)
	if !c.hasAudio {
		t.Fatal("native AAC audio must remain enabled")
	}
	if c.aacEnc != nil {
		t.Fatal("native AAC path must not attach a transcoding encoder")
	}
}
