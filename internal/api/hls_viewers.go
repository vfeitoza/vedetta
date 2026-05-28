package api

import (
	"sync"
	"time"
)

// hlsViewerTTL bounds how long a client without a fresh poll is still treated
// as an active HLS viewer. AVPlayer and other HLS clients poll the playlist
// roughly every target-duration window (1-5s for live profiles), so 20s gives
// a healthy 4x margin without leaving phantom viewers visible for minutes
// after the client closes.
const hlsViewerTTL = 20 * time.Second

// hlsViewers tracks distinct active HLS clients per camera by remote address
// for /metrics. HLS is a poll-based transport with no long-lived connection,
// so the count is maintained by recording every playlist/segment request and
// expiring stale entries on read.
//
// The key is the request's RemoteAddr (host:port). Behind a reverse proxy
// each downstream HTTP/1.1 keep-alive connection presents as a distinct
// upstream socket, so per-client distinction is preserved without trusting
// X-Forwarded-For.
type hlsViewers struct {
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	last map[string]map[string]time.Time // camera -> client key -> lastSeen
}

func newHLSViewers() *hlsViewers {
	return &hlsViewers{
		ttl:  hlsViewerTTL,
		now:  time.Now,
		last: make(map[string]map[string]time.Time),
	}
}

// seen records a fresh poll from client for camera. Empty arguments are
// silently ignored so callers can hand in a header value without guarding.
func (v *hlsViewers) seen(camera, client string) {
	if camera == "" || client == "" {
		return
	}
	t := v.now()
	v.mu.Lock()
	m, ok := v.last[camera]
	if !ok {
		m = make(map[string]time.Time)
		v.last[camera] = m
	}
	m[client] = t
	v.mu.Unlock()
}

// counts returns the active viewer count per camera, evicting entries older
// than the TTL window. A camera with zero active viewers is omitted so
// /metrics emits no stale zero series.
func (v *hlsViewers) counts() map[string]int {
	v.mu.Lock()
	defer v.mu.Unlock()
	cutoff := v.now().Add(-v.ttl)
	out := make(map[string]int, len(v.last))
	for cam, clients := range v.last {
		for k, t := range clients {
			if t.Before(cutoff) {
				delete(clients, k)
			}
		}
		if len(clients) == 0 {
			delete(v.last, cam)
			continue
		}
		out[cam] = len(clients)
	}
	return out
}
