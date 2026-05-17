package stream

import (
	"errors"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
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
		// Real iOS hardware AVPlayer refuses an HLS fMP4 rendition whose
		// AAC track is 8 kHz and disables the whole variant (the macOS
		// Simulator's software decoder tolerates it, which masked this).
		// The G.711 8 kHz source is therefore upsampled and the encoder,
		// the AudioSpecificConfig, and the fMP4 timescale must all run at
		// the real-device-safe target rate, never the 8 kHz camera rate.
		if rate != hlsTranscodeSampleRate || channels != 1 {
			t.Fatalf("encoder requested with rate=%d channels=%d, want %d/1",
				rate, channels, hlsTranscodeSampleRate)
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
	if c.aacConfig.SampleRate != hlsTranscodeSampleRate {
		t.Fatalf("AAC config sample rate = %d, want %d (real-device-safe target)",
			c.aacConfig.SampleRate, hlsTranscodeSampleRate)
	}
	if c.audioTimeScale != uint32(hlsTranscodeSampleRate) {
		t.Fatalf("audio timescale = %d, want %d (transcoded target rate)",
			c.audioTimeScale, hlsTranscodeSampleRate)
	}

	// Drive the transcode path. One RTP packet carrying 2048 µ-law bytes
	// decodes to 2048 PCM samples at 8 kHz; upsampled 6x to 48 kHz that is
	// 12288 samples, which the encoder turns into twelve 1024-sample AAC
	// frames.
	c.videoReady = true
	c.hasFirstKeyframe = true
	c.OnAudioRTP(&rtp.Packet{Payload: make([]byte, 2048)})

	factor := hlsTranscodeSampleRate / 8000
	wantFed := 2048 * factor
	if enc.fed != wantFed {
		t.Fatalf("encoder fed %d PCM samples, want %d (2048 decoded, upsampled %dx)",
			enc.fed, wantFed, factor)
	}
	if len(c.segAudio) != wantFed/1024 {
		t.Fatalf("expected %d transcoded AAC samples in the segment, got %d",
			wantFed/1024, len(c.segAudio))
	}
	if c.segAudio[0].Duration != hlsAudioSampleDuration {
		t.Fatalf("AAC sample duration = %d, want %d", c.segAudio[0].Duration, hlsAudioSampleDuration)
	}
}

// TestHLSTranscodeAudioStaysVideoLocked drives the G.711->AAC transcode
// path over many segments with a camera whose video RTP clock and audio
// sample clock are NOT coherent (the real garage camera: video advances
// 2.0333 s per segment while only 2.0 s of G.711 audio arrives). The AAC
// encoder's 1024-sample frame count free-runs independently of the video
// RTP clock, so accumulating audio sample ticks lets the audio fragment
// timeline drift away from video without bound. Real iOS AVPlayer cannot
// sustain a progressively desyncing fMP4 and falls back to snapshots
// (macOS AVFoundation tolerates it, which masked this). The transcoded
// audio fragment base must therefore stay locked to the video timeline.
func TestHLSTranscodeAudioStaysVideoLocked(t *testing.T) {
	withFakeEncoder(t, func(rate, channels int) (aacEncoder, error) {
		return &fakeAACEncoder{}, nil
	})

	audio := &rtsp.TrackInfo{Codec: "PCMU", ClockRate: 8000, ChannelCount: 1}
	c := newHLSConsumer(&rtsp.TrackInfo{Codec: "H264"}, audio)
	c.videoReady = true
	c.hasFirstKeyframe = true

	// Real garage camera cadence: video RTP advances 2.0333 s per segment
	// (183000 ticks @ 90 kHz) while exactly 2.0 s of 8 kHz G.711 arrives
	// (16000 samples) for that same wall-clock interval.
	const (
		segments       = 20
		videoTicksPerS = 183000
		g711BytesPerS  = 16000
	)
	var rtpTS uint32
	for i := 0; i < segments; i++ {
		c.segVideo = append(c.segVideo, &fmp4.Sample{
			Duration: videoTicksPerS,
			Payload:  []byte{0x00, 0x00, 0x00, 0x01, 0x65},
		})
		c.segVideoTicks += videoTicksPerS
		c.OnAudioRTP(&rtp.Packet{
			Header:  rtp.Header{Timestamp: rtpTS},
			Payload: make([]byte, g711BytesPerS),
		})
		rtpTS += g711BytesPerS
		c.closeSegmentLocked()
	}

	// The two fragment timelines must stay locked: the audio base may lag
	// or lead by at most one segment's correction, never accumulate. With
	// the free-running sample counter the offset grows ~0.05 s/segment and
	// is ~0.99 s after 20 segments.
	videoSec := float64(c.videoDTS) / 90000.0
	audioSec := float64(c.audioDTS) / float64(c.audioTimeScale)
	offset := videoSec - audioSec
	if offset < 0 {
		offset = -offset
	}
	if offset > 0.10 {
		t.Fatalf("audio drifted %.3f s from video over %d segments "+
			"(videoDTS=%d @90k = %.3fs, audioDTS=%d @%d = %.3fs); "+
			"transcoded audio fragment base must stay locked to video",
			offset, segments, c.videoDTS, videoSec,
			c.audioDTS, c.audioTimeScale, audioSec)
	}
}

func TestUpsamplePCMLinearInteger(t *testing.T) {
	// Integer-factor linear interpolation: N input samples become N*factor
	// output samples, endpoints preserved and the midpoint interpolated.
	out := upsamplePCM([]int16{0, 600}, 6)
	if len(out) != 12 {
		t.Fatalf("upsampled length = %d, want 12 (2 * 6)", len(out))
	}
	if out[0] != 0 {
		t.Errorf("out[0] = %d, want 0 (first sample preserved)", out[0])
	}
	if out[3] != 300 {
		t.Errorf("out[3] = %d, want 300 (linear midpoint between 0 and 600)", out[3])
	}
	if out[6] != 600 {
		t.Errorf("out[6] = %d, want 600 (second input sample)", out[6])
	}
	if out[11] != 600 {
		t.Errorf("out[11] = %d, want 600 (held past the last input sample)", out[11])
	}

	// factor 1 is the identity (defensive: never resample when not needed).
	id := upsamplePCM([]int16{1, 2, 3}, 1)
	if len(id) != 3 || id[0] != 1 || id[2] != 3 {
		t.Fatalf("factor 1 must be identity, got %v", id)
	}

	// Empty input stays empty, never panics.
	if got := upsamplePCM(nil, 6); len(got) != 0 {
		t.Fatalf("empty input must yield empty output, got %v", got)
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
