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

// iOS native HLS must default to the sub-stream: AVFoundation cold-warms
// the sub-stream in ~1s vs ~7s for the main/record stream and plays it
// with zero stalls, whereas the main stream flaps (server logs "buffer
// length exceeds 255") and AVFoundation cannot recover, cascading the
// client to ~1fps snapshots. ?quality=high opts back into full-res.
func TestPickHLSRTSPURL(t *testing.T) {
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
		{"quality=low picks sub-stream", "low", sub},
		{"unknown quality picks sub-stream", "medium", sub},
		{"quality=high picks main stream", "high", main},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickHLSRTSPURL(sub, main, tt.quality); got != tt.want {
				t.Errorf("pickHLSRTSPURL(%q, %q, %q) = %q, want %q",
					sub, main, tt.quality, got, tt.want)
			}
		})
	}
}
