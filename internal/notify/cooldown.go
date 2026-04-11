package notify

import (
	"sync"
	"time"
)

// CooldownCache tracks the last time a given notification key was delivered.
// It is intentionally a pure in-memory structure — the event log is the
// authoritative record of detections, and a cooldown miss across a restart
// is acceptable.
//
// Keys are opaque strings; callers format them as "<user>:<camera>:<class>".
// The cache is also used with "backoff:<endpoint-hash>" keys to implement
// 429 backoff per endpoint — see dispatcher.go.
type CooldownCache struct {
	window time.Duration
	clock  func() time.Time
	mu     sync.Mutex
	last   map[string]time.Time
}

// NewCooldownCache builds a cache with the given window.
// If clock is nil, time.Now is used.
func NewCooldownCache(window time.Duration, clock func() time.Time) *CooldownCache {
	if clock == nil {
		clock = time.Now
	}
	return &CooldownCache{
		window: window,
		clock:  clock,
		last:   make(map[string]time.Time),
	}
}

// Check returns true if the key is currently suppressed (within its window).
func (c *CooldownCache) Check(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ts, ok := c.last[key]
	if !ok {
		return false
	}
	return c.clock().Sub(ts) < c.window
}

// Mark records a successful delivery for the key at the current time.
func (c *CooldownCache) Mark(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last[key] = c.clock()
}

// Sweep removes entries older than 2× window. Intended for a periodic
// goroutine; safe to call concurrently with Check/Mark.
func (c *CooldownCache) Sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := c.clock().Add(-2 * c.window)
	for k, ts := range c.last {
		if ts.Before(cutoff) {
			delete(c.last, k)
		}
	}
}

// Size returns the number of tracked keys (for metrics and tests).
func (c *CooldownCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.last)
}
