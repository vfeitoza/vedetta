package api

import "testing"

func TestPickWebRTCRTSPURL(t *testing.T) {
	const (
		sub  = "rtsp://cam/stream2"
		main = "rtsp://cam/stream1"
	)
	tests := []struct {
		name    string
		quality string
		want    string
	}{
		{"default picks sub-stream", "", sub},
		{"unknown quality picks sub-stream", "medium", sub},
		{"quality=high picks main stream", "high", main},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickWebRTCRTSPURL(sub, main, tt.quality); got != tt.want {
				t.Errorf("pickWebRTCRTSPURL(%q, %q, %q) = %q, want %q",
					sub, main, tt.quality, got, tt.want)
			}
		})
	}
}
