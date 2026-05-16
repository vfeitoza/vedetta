package stream

import (
	"testing"

	"github.com/rvben/vedetta/internal/config"
)

func TestCameraStreamCapabilities(t *testing.T) {
	disabled := false
	cams := []config.CameraConfig{
		{
			Name:      "front_door",
			URL:       "rtsp://cam/sub",
			RecordURL: "rtsp://cam/main",
		},
		{
			Name: "garage", // no distinct sub-stream
			URL:  "rtsp://cam2/only",
		},
		{
			Name:    "old_cam",
			URL:     "rtsp://cam3/x",
			Enabled: &disabled,
		},
	}

	t.Run("rtsp enabled", func(t *testing.T) {
		got := CameraStreamCapabilities(cams, config.RTSPServerConfig{Enabled: true, Port: 8554}, "vedetta.lan")

		if len(got) != 2 {
			t.Fatalf("expected 2 enabled cameras, got %d: %+v", len(got), got)
		}

		fd := got[0]
		if fd.Name != "front_door" {
			t.Fatalf("got[0].Name = %q, want front_door", fd.Name)
		}
		if fd.Streams.RTSPMain != "rtsp://vedetta.lan:8554/front_door" {
			t.Errorf("RTSPMain = %q", fd.Streams.RTSPMain)
		}
		if fd.Streams.RTSPSub != "rtsp://vedetta.lan:8554/front_door_sub" {
			t.Errorf("RTSPSub = %q", fd.Streams.RTSPSub)
		}
		if fd.Streams.WebRTC != "/api/cameras/front_door/webrtc/offer" {
			t.Errorf("WebRTC = %q", fd.Streams.WebRTC)
		}

		garage := got[1]
		if garage.Streams.RTSPMain != "rtsp://vedetta.lan:8554/garage" {
			t.Errorf("garage RTSPMain = %q", garage.Streams.RTSPMain)
		}
		if garage.Streams.RTSPSub != "" {
			t.Errorf("garage should have no sub-stream, got %q", garage.Streams.RTSPSub)
		}
	})

	t.Run("rtsp disabled", func(t *testing.T) {
		got := CameraStreamCapabilities(cams, config.RTSPServerConfig{Enabled: false}, "vedetta.lan")

		if len(got) != 2 {
			t.Fatalf("expected 2 enabled cameras, got %d", len(got))
		}
		for _, s := range got {
			if s.Streams.RTSPMain != "" || s.Streams.RTSPSub != "" {
				t.Errorf("%s: RTSP URLs must be empty when server disabled, got main=%q sub=%q",
					s.Name, s.Streams.RTSPMain, s.Streams.RTSPSub)
			}
			if s.Streams.HLS == "" || s.Streams.MJPEG == "" || s.Streams.MSE == "" ||
				s.Streams.Snapshot == "" || s.Streams.WebRTC == "" {
				t.Errorf("%s: HTTP stream paths must always be present: %+v", s.Name, s)
			}
		}
	})
}
