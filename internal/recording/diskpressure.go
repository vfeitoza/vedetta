package recording

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// DiskLevel describes the current disk pressure state.
type DiskLevel int

const (
	// DiskLevelOK means available space is above 2× the minimum threshold.
	DiskLevelOK DiskLevel = iota
	// DiskLevelWarning means available space is between 1× and 2× the threshold.
	DiskLevelWarning
	// DiskLevelCritical means available space is below the minimum threshold.
	DiskLevelCritical
	// DiskLevelPaused means recording has been halted due to disk pressure.
	DiskLevelPaused
)

func (l DiskLevel) String() string {
	switch l {
	case DiskLevelOK:
		return "ok"
	case DiskLevelWarning:
		return "warning"
	case DiskLevelCritical:
		return "critical"
	case DiskLevelPaused:
		return "paused"
	default:
		return "unknown"
	}
}

// DiskSampler is the interface DiskMonitor uses to read current disk metrics.
// *media.DiskSpace satisfies this interface.
type DiskSampler interface {
	Available() uint64
	MinRequired() uint64
	Total() uint64
}

// DiskEvent is emitted by DiskMonitor whenever the disk level changes.
type DiskEvent struct {
	Level     DiskLevel
	Available uint64
	Total     uint64
}

// DiskMonitor classifies disk pressure and notifies subscribers when the
// level transitions. It runs a periodic tick loop via Run.
type DiskMonitor struct {
	sampler DiskSampler
	paused  atomic.Bool

	mu          sync.Mutex
	lastLevel   DiskLevel
	subscribers []chan<- DiskEvent
}

// NewDiskMonitor creates a DiskMonitor backed by the given sampler.
func NewDiskMonitor(sampler DiskSampler) *DiskMonitor {
	return &DiskMonitor{
		sampler:   sampler,
		lastLevel: DiskLevelOK,
	}
}

// Classify returns the current disk level without mutating state. It is safe
// to call concurrently and does not notify subscribers.
func (m *DiskMonitor) Classify() DiskLevel {
	if m.paused.Load() {
		return DiskLevelPaused
	}
	avail := m.sampler.Available()
	thresh := m.sampler.MinRequired()
	switch {
	case avail < thresh:
		return DiskLevelCritical
	case avail < 2*thresh:
		return DiskLevelWarning
	default:
		return DiskLevelOK
	}
}

// SetPaused marks the monitor as paused or unpaused. When paused, Classify
// always returns DiskLevelPaused regardless of actual disk space.
func (m *DiskMonitor) SetPaused(paused bool) {
	m.paused.Store(paused)
}

// Subscribe registers a channel to receive DiskEvent notifications on level
// transitions. The channel must be buffered; drops are logged but non-blocking.
func (m *DiskMonitor) Subscribe(ch chan<- DiskEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers = append(m.subscribers, ch)
}

// Run starts the periodic sampling loop. It samples immediately, then
// every interval until ctx is cancelled.
func (m *DiskMonitor) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	m.sampleOnce()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sampleOnce()
		}
	}
}

func (m *DiskMonitor) sampleOnce() {
	level := m.Classify()

	m.mu.Lock()
	if level == m.lastLevel {
		m.mu.Unlock()
		return
	}
	m.lastLevel = level
	subs := append([]chan<- DiskEvent(nil), m.subscribers...)
	m.mu.Unlock()

	avail := m.sampler.Available()
	total := m.sampler.Total()
	ev := DiskEvent{Level: level, Available: avail, Total: total}

	switch level {
	case DiskLevelCritical, DiskLevelPaused:
		slog.Error("disk pressure level changed",
			"level", level,
			"available_bytes", avail,
			"total_bytes", total,
		)
	case DiskLevelWarning:
		slog.Warn("disk pressure level changed",
			"level", level,
			"available_bytes", avail,
			"total_bytes", total,
		)
	default:
		slog.Info("disk pressure level changed",
			"level", level,
			"available_bytes", avail,
			"total_bytes", total,
		)
	}

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			slog.Warn("disk event subscriber channel full, dropping event", "level", level)
		}
	}
}
