package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rvben/vedetta/internal/config"
)

// The browser is the WebRTC offerer, so its RTCPeerConnection ICE servers are
// not signaled by the server's answer - the client must fetch them. The
// privacy-first default means this endpoint returns an empty list (never a
// hardcoded public STUN), so a default install leaks no viewer IP to a third
// party.
func TestGetWebRTCICEServers_DefaultEmpty(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/streaming/ice-servers", nil)
	srv.GetWebRTCICEServers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "stun.l.google.com") {
		t.Fatalf("default response must not advertise a public STUN server: %s", rec.Body.String())
	}
	var resp struct {
		ICEServers []config.ICEServerConfig `json:"ice_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if len(resp.ICEServers) != 0 {
		t.Fatalf("default ICE servers must be empty, got %+v", resp.ICEServers)
	}
	// Must serialize as [] not null so the browser can spread it directly.
	if !strings.Contains(rec.Body.String(), `"ice_servers":[]`) {
		t.Fatalf("empty list must serialize as [], got %s", rec.Body.String())
	}
}

func TestGetWebRTCICEServers_ReturnsConfigured(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.webrtcConfig = config.WebRTCConfig{ICEServers: []config.ICEServerConfig{
		{URLs: []string{"stun:stun.example.net:3478"}},
		{URLs: []string{"turn:turn.example.net:3478"}, Username: "u", Credential: "p"},
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/streaming/ice-servers", nil)
	srv.GetWebRTCICEServers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		ICEServers []config.ICEServerConfig `json:"ice_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.ICEServers) != 2 {
		t.Fatalf("expected 2 ICE servers, got %d", len(resp.ICEServers))
	}
	if resp.ICEServers[0].URLs[0] != "stun:stun.example.net:3478" || resp.ICEServers[0].Username != "" {
		t.Fatalf("stun entry mismapped: %+v", resp.ICEServers[0])
	}
	if resp.ICEServers[1].Username != "u" || resp.ICEServers[1].Credential != "p" {
		t.Fatalf("turn credentials mismapped: %+v", resp.ICEServers[1])
	}
}

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
