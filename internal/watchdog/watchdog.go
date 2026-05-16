// Package watchdog provides a heartbeat-based liveness guard. When the
// process stops making progress (a deadlock or a stuck processing loop) the
// watchdog forces termination so the supervisor (launchd KeepAlive) can
// restart it, turning an open-ended grey failure into a brief restart.
package watchdog

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// Watchdog fires an action when no Kick has occurred within the timeout.
type Watchdog struct {
	timeout   time.Duration
	onTimeout func(stalled time.Duration)
	lastKick  atomic.Int64 // UnixNano of the most recent kick
}

// New creates a Watchdog with the given inactivity timeout. onTimeout is
// invoked once when the deadline is exceeded; it receives how long the
// process has been stalled.
func New(timeout time.Duration, onTimeout func(stalled time.Duration)) *Watchdog {
	w := &Watchdog{timeout: timeout, onTimeout: onTimeout}
	w.lastKick.Store(time.Now().UnixNano())
	return w
}

// NewProcessGuard returns a Watchdog whose timeout action logs the stall and
// terminates the process so the supervisor can restart it.
func NewProcessGuard(timeout time.Duration) *Watchdog {
	return New(timeout, func(stalled time.Duration) {
		slog.Error("liveness watchdog tripped, terminating for supervisor restart",
			"stalled", stalled.Round(time.Second),
			"timeout", timeout,
		)
		os.Exit(1)
	})
}

// Kick records a heartbeat. Safe for concurrent use.
func (w *Watchdog) Kick() {
	w.lastKick.Store(time.Now().UnixNano())
}

// Run blocks until ctx is cancelled, checking liveness on an interval derived
// from the timeout. It fires onTimeout at most once per stall.
func (w *Watchdog) Run(ctx context.Context) {
	interval := w.timeout / 4
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stalled := time.Duration(time.Now().UnixNano() - w.lastKick.Load())
			if stalled > w.timeout {
				w.onTimeout(stalled)
				return
			}
		}
	}
}
