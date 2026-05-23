package stream

import (
	"testing"

	"github.com/pion/webrtc/v4"

	"github.com/rvben/vedetta/internal/config"
)

// The privacy-first default: with no configured ICE servers, WebRTC must offer
// no external STUN/TURN at all. A hardcoded public STUN server (e.g. Google's)
// leaks every viewer's IP on each connection attempt. LAN peers connect via
// host candidates without STUN.
func TestICEServersFromConfig_DefaultIsNoSTUN(t *testing.T) {
	if got := iceServersFromConfig(nil); len(got) != 0 {
		t.Fatalf("nil config must yield zero ICE servers (no IP leak), got %v", got)
	}
	if got := iceServersFromConfig([]config.ICEServerConfig{}); len(got) != 0 {
		t.Fatalf("empty config must yield zero ICE servers, got %v", got)
	}
}

func TestICEServersFromConfig_MapsStunAndTurn(t *testing.T) {
	got := iceServersFromConfig([]config.ICEServerConfig{
		{URLs: []string{"stun:stun.example.net:3478"}},
		{URLs: []string{"turn:turn.example.net:3478"}, Username: "u", Credential: "p"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 ICE servers, got %d", len(got))
	}
	if len(got[0].URLs) != 1 || got[0].URLs[0] != "stun:stun.example.net:3478" {
		t.Fatalf("stun URLs mismapped: %+v", got[0])
	}
	if got[0].Username != "" || got[0].Credential != nil {
		t.Fatalf("stun server must carry no credentials: %+v", got[0])
	}
	if got[1].Username != "u" || got[1].Credential != "p" {
		t.Fatalf("turn credentials mismapped: %+v", got[1])
	}
	if got[1].CredentialType != webrtc.ICECredentialTypePassword {
		t.Fatalf("turn credential type should be password, got %v", got[1].CredentialType)
	}
}

// An entry with no URLs is meaningless to pion and would fail ICE setup; skip it
// rather than pass a malformed server through.
func TestICEServersFromConfig_SkipsEntriesWithoutURLs(t *testing.T) {
	got := iceServersFromConfig([]config.ICEServerConfig{
		{URLs: nil, Username: "u", Credential: "p"},
		{URLs: []string{"stun:stun.example.net:3478"}},
	})
	if len(got) != 1 {
		t.Fatalf("entries without URLs must be skipped, got %d servers", len(got))
	}
}

func TestNewStreamManager_StoresConfiguredICEServers(t *testing.T) {
	sm := NewStreamManager(nil, []config.ICEServerConfig{
		{URLs: []string{"stun:stun.example.net:3478"}},
	})
	if len(sm.iceServers) != 1 || sm.iceServers[0].URLs[0] != "stun:stun.example.net:3478" {
		t.Fatalf("StreamManager did not store configured ICE servers: %+v", sm.iceServers)
	}
}

func TestNewStreamManager_NoConfigYieldsNoICEServers(t *testing.T) {
	sm := NewStreamManager(nil, nil)
	if len(sm.iceServers) != 0 {
		t.Fatalf("default StreamManager must have no ICE servers, got %+v", sm.iceServers)
	}
}
