package media

import (
	"log/slog"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/rtsp"
)

// RawFrame holds a decoded RGB24 frame for detection.
type RawFrame struct {
	Data   []byte
	Width  int
	Height int
}

// DetectConsumer implements rtsp.Consumer and decodes H264 keyframes to RGB24.
type DetectConsumer struct {
	width  int
	height int
	camera string

	h264Decoder *rtph264.Decoder
	h264Dec     *H264Decoder
	sps         []byte
	pps         []byte

	mu         sync.Mutex
	frameCh    chan RawFrame
	lastFrame  time.Time
	frameDelay time.Duration
	frameCount uint64
	lastLog    time.Time
	rtpCount   uint64
	auCount    uint64
	idrCount   uint64
}

// NewDetectConsumer creates a consumer that decodes H264 keyframes for detection.
// Detection is disabled if OpenH264 is unavailable.
func NewDetectConsumer(camera string, width, height, fps int, track *rtsp.TrackInfo) *DetectConsumer {
	dc := &DetectConsumer{
		width:      width,
		height:     height,
		camera:     camera,
		frameCh:    make(chan RawFrame, 2),
		frameDelay: time.Second / time.Duration(max(fps, 1)),
		lastLog:    time.Now(),
	}

	if track == nil || track.Codec != "H264" {
		slog.Warn("detection disabled: track is not H264")
		return dc
	}

	dc.sps = track.SPS
	dc.pps = track.PPS

	h264Format := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               track.SPS,
		PPS:               track.PPS,
	}
	dec, err := h264Format.CreateDecoder()
	if err != nil {
		slog.Warn("failed to create H264 RTP decoder for detection", "error", err)
		return dc
	}
	dc.h264Decoder = dec

	dc.h264Dec = NewH264Decoder()
	if dc.h264Dec == nil {
		slog.Warn("detection disabled: OpenH264 unavailable (auto-download may have failed)")
		return dc
	}

	slog.Info("detection enabled with OpenH264 decode")
	return dc
}

// Frames returns the channel of decoded frames.
func (dc *DetectConsumer) Frames() <-chan RawFrame {
	return dc.frameCh
}

// Close releases decoder resources.
func (dc *DetectConsumer) Close() {
	if dc.h264Dec != nil {
		dc.h264Dec.Close()
		dc.h264Dec = nil
	}
}

// OnVideoRTP processes a video RTP packet, decoding keyframes to RGB24.
func (dc *DetectConsumer) OnVideoRTP(pkt *rtp.Packet) {
	if dc.h264Decoder == nil || dc.h264Dec == nil {
		return
	}

	dc.mu.Lock()
	dc.rtpCount++
	dc.mu.Unlock()

	au, err := dc.h264Decoder.Decode(pkt)
	if err != nil {
		return
	}

	dc.mu.Lock()
	dc.auCount++
	dc.mu.Unlock()

	// Periodic status log — fires whether or not frames are being decoded
	dc.mu.Lock()
	if time.Since(dc.lastLog) >= 5*time.Minute {
		slog.Info("detection status",
			"camera", dc.camera,
			"rtp_packets", dc.rtpCount,
			"access_units", dc.auCount,
			"idr_frames", dc.idrCount,
			"frames_decoded", dc.frameCount,
		)
		dc.frameCount = 0
		dc.rtpCount = 0
		dc.auCount = 0
		dc.idrCount = 0
		dc.lastLog = time.Now()
	}
	// Rate limit
	if time.Since(dc.lastFrame) < dc.frameDelay {
		dc.mu.Unlock()
		return
	}
	dc.mu.Unlock()

	if !h264.IsRandomAccess(au) {
		return
	}

	dc.mu.Lock()
	dc.idrCount++
	dc.mu.Unlock()

	// Update SPS/PPS from in-band parameters
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		typ := h264.NALUType(nalu[0] & 0x1F)
		switch typ {
		case h264.NALUTypeSPS:
			dc.sps = nalu
		case h264.NALUTypePPS:
			dc.pps = nalu
		}
	}

	if dc.sps == nil {
		return
	}

	// Build NAL unit stream with start codes for OpenH264.
	// Always prepend SPS/PPS before IDR if not already present in the AU,
	// as some cameras (Tapo) send IDR without inline parameter sets.
	var nalStream []byte
	startCode := []byte{0, 0, 0, 1}

	hasSPS := false
	for _, nalu := range au {
		if len(nalu) > 0 && h264.NALUType(nalu[0]&0x1F) == h264.NALUTypeSPS {
			hasSPS = true
			break
		}
	}
	if !hasSPS && dc.sps != nil {
		nalStream = append(nalStream, startCode...)
		nalStream = append(nalStream, dc.sps...)
		if dc.pps != nil {
			nalStream = append(nalStream, startCode...)
			nalStream = append(nalStream, dc.pps...)
		}
	}

	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		nalStream = append(nalStream, startCode...)
		nalStream = append(nalStream, nalu...)
	}

	ycbcr := dc.h264Dec.Decode(nalStream)
	if ycbcr == nil {
		return
	}

	rgb24 := ycbcrToRGB24Scaled(ycbcr, dc.width, dc.height)

	dc.mu.Lock()
	dc.lastFrame = time.Now()
	dc.frameCount++
	dc.mu.Unlock()

	select {
	case dc.frameCh <- RawFrame{Data: rgb24, Width: dc.width, Height: dc.height}:
	default:
	}
}

// OnAudioRTP is a no-op for detection.
func (dc *DetectConsumer) OnAudioRTP(_ *rtp.Packet) {}

// OnDisconnect is called when the source disconnects.
func (dc *DetectConsumer) OnDisconnect() {}
