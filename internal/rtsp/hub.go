package rtsp

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// SanitizeURL removes credentials from an RTSP URL for safe logging.
func SanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "rtsp://***@<invalid>"
	}
	if u.User == nil {
		// Continue to query/fragment redaction even without userinfo.
	} else {
		u.User = nil
	}
	if u.RawQuery != "" {
		q := u.Query()
		changed := false
		for key := range q {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "pass") || strings.Contains(lower, "token") ||
				strings.Contains(lower, "secret") || strings.Contains(lower, "key") ||
				strings.Contains(lower, "sig") {
				q.Set(key, "REDACTED")
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
	}
	u.Fragment = ""
	return u.String()
}

// Hub manages one Source per RTSP URL, ensuring a single connection per stream.
type Hub struct {
	mu      sync.Mutex
	sources map[string]*managedSource
	ctx     context.Context
	cancel  context.CancelFunc

	// reconnectSinks maps an RTSP URL to the per-camera counters that want every
	// reconnect on that URL. Registration is independent of source creation: a
	// Source created later (by recording, snapshot, or detect, whichever opens
	// the stream first) is wired up from this registry, and a Source recreated
	// after Remove re-wires the same sinks, keeping the counters monotonic. One
	// URL can map to several counters because multiple cameras may share it.
	reconnectSinks map[string][]*atomic.Int64
}

type managedSource struct {
	source *Source
	cancel context.CancelFunc
}

// NewHub creates a new RTSP hub.
func NewHub(ctx context.Context) *Hub {
	ctx, cancel := context.WithCancel(ctx)
	return &Hub{
		sources:        make(map[string]*managedSource),
		reconnectSinks: make(map[string][]*atomic.Int64),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// GetOrCreate returns the Source for the given URL, creating and connecting it if needed.
func (h *Hub) GetOrCreate(url string) *Source {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ms, ok := h.sources[url]; ok {
		return ms.source
	}

	src := NewSource(url)
	srcCtx, srcCancel := context.WithCancel(h.ctx)
	for _, sink := range h.reconnectSinks[url] {
		src.AddReconnectSink(sink)
	}

	h.sources[url] = &managedSource{
		source: src,
		cancel: srcCancel,
	}

	go src.Connect(srcCtx)

	slog.Info("RTSP hub created source", "url", SanitizeURL(url))
	return src
}

// RegisterReconnectSink records that the given counter wants every reconnect on
// url, and wires it onto the live Source if one already exists. The registration
// outlives individual Sources, so a Source created or recreated later (after
// Remove) picks the sink up automatically. Idempotent per (url, sink) so a
// camera restart cannot double-register.
func (h *Hub) RegisterReconnectSink(url string, sink *atomic.Int64) {
	if url == "" || sink == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, existing := range h.reconnectSinks[url] {
		if existing == sink {
			return
		}
	}
	h.reconnectSinks[url] = append(h.reconnectSinks[url], sink)
	if ms, ok := h.sources[url]; ok {
		ms.source.AddReconnectSink(sink)
	}
}

// Get returns the Source for the given URL, or nil if it doesn't exist.
func (h *Hub) Get(url string) *Source {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ms, ok := h.sources[url]; ok {
		return ms.source
	}
	return nil
}

// Remove disconnects and removes the Source for the given URL.
// If no consumers remain, this frees the RTSP connection slot on the camera.
func (h *Hub) Remove(url string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ms, ok := h.sources[url]; ok {
		ms.cancel()
		delete(h.sources, url)
		slog.Info("RTSP hub removed source", "url", SanitizeURL(url))
	}
}

// Close disconnects all sources and shuts down the hub.
func (h *Hub) Close() {
	h.cancel()

	h.mu.Lock()
	defer h.mu.Unlock()

	for url, ms := range h.sources {
		ms.cancel()
		delete(h.sources, url)
	}

	slog.Info("RTSP hub closed")
}
