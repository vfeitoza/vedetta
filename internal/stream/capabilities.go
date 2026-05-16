package stream

import (
	"fmt"

	"github.com/rvben/vedetta/internal/config"
)

// CameraStreams is the protocol-by-protocol inventory of how one camera can
// be consumed. RTSP fields are absolute URLs (empty when the RTSP republish
// server is disabled, or RTSPSub when the camera has no distinct sub-stream).
// HTTP fields are always paths relative to the API base. JSON tags mirror the
// CameraStreamURLs schema so the CLI and HTTP API serialize identically.
type CameraStreams struct {
	RTSPMain string `json:"rtsp_main,omitempty"`
	RTSPSub  string `json:"rtsp_sub,omitempty"`
	WebRTC   string `json:"webrtc,omitempty"`
	HLS      string `json:"hls,omitempty"`
	MJPEG    string `json:"mjpeg,omitempty"`
	MSE      string `json:"mse,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// CameraStreamSet pairs a camera name with its consumable stream URLs.
type CameraStreamSet struct {
	Name    string        `json:"name"`
	Streams CameraStreams `json:"streams"`
}

// CameraStreamCapabilities derives the consumable stream inventory for every
// enabled camera. The sub-stream rule mirrors NewRTSPServer exactly: a
// `_sub` path is published only when the camera has a distinct low-res URL
// (RecordURL set and different from URL). rtspHost is the host clients should
// dial for RTSP (the API request host, or a configured/placeholder host for
// the CLI); it is only used when rtspCfg.Enabled.
func CameraStreamCapabilities(cams []config.CameraConfig, rtspCfg config.RTSPServerConfig, rtspHost string) []CameraStreamSet {
	out := make([]CameraStreamSet, 0, len(cams))
	for _, c := range cams {
		if !c.IsEnabled() {
			continue
		}
		s := CameraStreamSet{
			Name: c.Name,
			Streams: CameraStreams{
				WebRTC:   "/api/cameras/" + c.Name + "/webrtc/offer",
				HLS:      "/api/cameras/" + c.Name + "/live.m3u8",
				MJPEG:    "/api/cameras/" + c.Name + "/mjpeg",
				MSE:      "/api/cameras/" + c.Name + "/mse/ws",
				Snapshot: "/api/cameras/" + c.Name + "/snapshot",
			},
		}
		if rtspCfg.Enabled {
			base := fmt.Sprintf("rtsp://%s:%d", rtspHost, rtspCfg.Port)
			s.Streams.RTSPMain = base + "/" + c.Name
			if c.RecordURL != "" && c.URL != c.RecordURL {
				s.Streams.RTSPSub = base + "/" + c.Name + "_sub"
			}
		}
		out = append(out, s)
	}
	return out
}
