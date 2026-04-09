package rtsp

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
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
}

type managedSource struct {
	source *Source
	cancel context.CancelFunc
}

// NewHub creates a new RTSP hub.
func NewHub(ctx context.Context) *Hub {
	ctx, cancel := context.WithCancel(ctx)
	return &Hub{
		sources: make(map[string]*managedSource),
		ctx:     ctx,
		cancel:  cancel,
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

	h.sources[url] = &managedSource{
		source: src,
		cancel: srcCancel,
	}

	go src.Connect(srcCtx)

	slog.Info("RTSP hub created source", "url", SanitizeURL(url))
	return src
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
