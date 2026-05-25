package rtsp

import (
	"context"
	"sync/atomic"
	"testing"
)

// canceledHub returns a hub whose context is already done, so GetOrCreate's
// per-source Connect goroutine exits immediately and never touches the reconnect
// counters. Reconnects are driven explicitly via SimulateReconnectForTest.
func canceledHub(t *testing.T) *Hub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hub := NewHub(ctx)
	t.Cleanup(hub.Close)
	return hub
}

// A reconnect sink registered before the source exists must be wired up when the
// source is later created, so the per-camera counter starts accumulating from
// the first drop regardless of which subsystem opens the stream.
func TestHubRegisterReconnectSink_WiresSourceCreatedLater(t *testing.T) {
	hub := canceledHub(t)
	url := "rtsp://test:554/cam"

	var counter atomic.Int64
	hub.RegisterReconnectSink(url, &counter)

	src := hub.GetOrCreate(url)
	src.SimulateReconnectForTest()

	if got := counter.Load(); got != 1 {
		t.Errorf("counter = %d, want 1 (sink registered before source creation)", got)
	}
}

// Registering after the source already exists must wire the live source too.
func TestHubRegisterReconnectSink_WiresExistingSource(t *testing.T) {
	hub := canceledHub(t)
	url := "rtsp://test:554/cam"

	src := hub.GetOrCreate(url)
	var counter atomic.Int64
	hub.RegisterReconnectSink(url, &counter)

	src.SimulateReconnectForTest()
	if got := counter.Load(); got != 1 {
		t.Errorf("counter = %d, want 1 (sink registered after source creation)", got)
	}
}

// Two cameras configured with the same RTSP URL share one Source. A reconnect on
// that shared connection must increment both cameras' counters, not just the
// last one registered.
func TestHubRegisterReconnectSink_SharedURLFansOutToAllCameras(t *testing.T) {
	hub := canceledHub(t)
	url := "rtsp://test:554/shared"

	var camA, camB atomic.Int64
	hub.RegisterReconnectSink(url, &camA)
	hub.RegisterReconnectSink(url, &camB)

	src := hub.GetOrCreate(url)
	src.SimulateReconnectForTest()

	if camA.Load() != 1 || camB.Load() != 1 {
		t.Errorf("shared-source reconnect: camA=%d camB=%d, want 1 and 1", camA.Load(), camB.Load())
	}
}

// The reconnect total must stay monotonic across a stop/start. Removing a source
// discards it (and its own counter), but the registered sink is re-wired when a
// fresh source is created, so the camera's cumulative count keeps climbing
// without the camera having to re-register.
func TestHubRegisterReconnectSink_SurvivesRemoveAndRecreate(t *testing.T) {
	hub := canceledHub(t)
	url := "rtsp://test:554/cam"

	var counter atomic.Int64
	hub.RegisterReconnectSink(url, &counter)

	src := hub.GetOrCreate(url)
	src.SimulateReconnectForTest()
	if got := counter.Load(); got != 1 {
		t.Fatalf("counter = %d, want 1 before removal", got)
	}

	hub.Remove(url)
	if got := counter.Load(); got != 1 {
		t.Errorf("counter = %d, want 1 after removal (must not reset)", got)
	}

	src2 := hub.GetOrCreate(url)
	src2.SimulateReconnectForTest()
	if got := counter.Load(); got != 2 {
		t.Errorf("counter = %d, want 2 after recreate + 1 drop (registry must re-wire)", got)
	}
}

// Re-registering the same counter (e.g. a camera restart calling Start again)
// must not double-count reconnects on a single shared source.
func TestHubRegisterReconnectSink_IdempotentPerSink(t *testing.T) {
	hub := canceledHub(t)
	url := "rtsp://test:554/cam"

	var counter atomic.Int64
	hub.RegisterReconnectSink(url, &counter)
	hub.RegisterReconnectSink(url, &counter)

	src := hub.GetOrCreate(url)
	src.SimulateReconnectForTest()

	if got := counter.Load(); got != 1 {
		t.Errorf("counter = %d, want 1 (duplicate registration must not double-count)", got)
	}
}

func TestSanitizeURL_RedactsCredentialsAndSecrets(t *testing.T) {
	raw := "rtsp://user:pass@example.com/live?token=abc123&profile=main#frag"
	got := SanitizeURL(raw)
	want := "rtsp://example.com/live?profile=main&token=REDACTED"
	if got != want {
		t.Fatalf("SanitizeURL() = %q, want %q", got, want)
	}
}

func TestSanitizeURL_Invalid(t *testing.T) {
	if got := SanitizeURL("://bad"); got != "rtsp://***@<invalid>" {
		t.Fatalf("SanitizeURL() = %q, want invalid placeholder", got)
	}
}

func TestHubGetOrCreate_ReturnsSameSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(ctx)
	defer hub.Close()

	url := "rtsp://test:554/stream1"
	s1 := hub.GetOrCreate(url)
	s2 := hub.GetOrCreate(url)

	if s1 != s2 {
		t.Fatal("GetOrCreate returned different sources for same URL")
	}
}

func TestHubGetOrCreate_DifferentURLs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(ctx)
	defer hub.Close()

	s1 := hub.GetOrCreate("rtsp://test:554/stream1")
	s2 := hub.GetOrCreate("rtsp://test:554/stream2")

	if s1 == s2 {
		t.Fatal("GetOrCreate returned same source for different URLs")
	}
}

func TestHubGet_ReturnsNilForUnknown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(ctx)
	defer hub.Close()

	if s := hub.Get("rtsp://nonexistent:554/stream"); s != nil {
		t.Fatal("Get returned non-nil for unknown URL")
	}
}

func TestHubGet_ReturnsExisting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(ctx)
	defer hub.Close()

	url := "rtsp://test:554/stream1"
	created := hub.GetOrCreate(url)
	got := hub.Get(url)

	if got != created {
		t.Fatal("Get didn't return the source created by GetOrCreate")
	}
}

func TestHubClose_ClearsAllSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := NewHub(ctx)
	hub.GetOrCreate("rtsp://test:554/stream1")
	hub.GetOrCreate("rtsp://test:554/stream2")

	hub.Close()

	if s := hub.Get("rtsp://test:554/stream1"); s != nil {
		t.Fatal("source still present after Close")
	}
}
