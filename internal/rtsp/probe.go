package rtsp

import (
	"context"
	"errors"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"

	"github.com/rvben/vedetta/internal/netguard"
)

// ProbeResult summarizes what an RTSP DESCRIBE returned.
type ProbeResult struct {
	VideoCodec string
	Width      int
	Height     int
	HasAudio   bool
	AudioCodec string
}

// Probe connects to the RTSP URL and issues DESCRIBE only (no PLAY).
// It honors the ctx deadline for connect and describe phases.
func Probe(ctx context.Context, rawURL string) (ProbeResult, error) {
	var result ProbeResult

	u, err := base.ParseURL(rawURL)
	if err != nil {
		return result, err
	}
	if u.Scheme != "rtsp" && u.Scheme != "rtsps" {
		return result, errors.New("URL scheme must be rtsp or rtsps")
	}

	proto := gortsplib.ProtocolTCP
	client := &gortsplib.Client{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Protocol: &proto,
		// Probe dials a user-supplied URL, so enforce the SSRF policy at
		// connect time against the resolved address (closes the DNS-rebinding
		// window left by a hostname pre-check). ctx carries the deadline.
		DialContext: netguard.Dialer(0).DialContext,
	}

	// Start() only initializes client state and spawns the internal run loop; it
	// does not dial, so calling it synchronously here cannot block on the
	// network. Doing so guarantees the client is fully initialized before any
	// Close(), which closes the data race between Close() (timeout path) and a
	// concurrent Start() in the probe goroutine. The dial and DESCRIBE happen on
	// the goroutine below and stay bounded by ctx: on timeout the deferred
	// Close() cancels the client, which interrupts an in-flight Describe.
	if err := client.Start(); err != nil {
		return result, err
	}
	defer client.Close()

	done := make(chan error, 1)
	var desc *description.Session
	go func() {
		d, _, err := client.Describe(u)
		if err != nil {
			done <- err
			return
		}
		desc = d
		done <- nil
	}()

	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case err := <-done:
		if err != nil {
			return result, err
		}
	}

	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			switch f := forma.(type) {
			case *format.H264:
				result.VideoCodec = "H264"
				if len(f.SPS) > 0 {
					var sps h264.SPS
					if err := sps.Unmarshal(f.SPS); err == nil {
						result.Width = sps.Width()
						result.Height = sps.Height()
					}
				}
			case *format.H265:
				if result.VideoCodec == "" {
					result.VideoCodec = "H265"
				}
			case *format.MPEG4Audio:
				result.HasAudio = true
				result.AudioCodec = "AAC"
			case *format.G711:
				result.HasAudio = true
				if f.MULaw {
					result.AudioCodec = "PCMU"
				} else {
					result.AudioCodec = "PCMA"
				}
			case *format.Opus:
				result.HasAudio = true
				result.AudioCodec = "Opus"
			}
		}
	}

	if result.VideoCodec == "" {
		return result, errors.New("no supported video track found")
	}
	return result, nil
}
