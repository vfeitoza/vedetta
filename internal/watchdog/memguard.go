package watchdog

import (
	"context"
	"runtime/metrics"
	"time"
)

// The memory guard turns an OS out-of-memory kill into a clean restart. A
// runaway leak would otherwise grow until the kernel's OOM killer (macOS
// jetsam, Linux oom-killer) SIGKILLs the process - uncatchable, so in-flight
// MP4 segments are never finalized and the only external signal is a
// ServiceDown alert. The guard trips first, at a configured footprint ceiling
// well below real memory pressure, and asks for a graceful shutdown so the
// supervisor restarts the process predictably.
//
// It measures Go-runtime memory (the heap, stacks, and other runtime classes),
// which is where a main-process leak accumulates. Memory allocated off-heap by C
// libraries (e.g. via purego) is not counted; the out-of-process supervisor and
// the OS OOM killer remain the backstop for that rarer case.
const (
	// memGuardInterval is how often the guard samples memory. The runaway this
	// guards against ramps the heap at roughly 10 GB/min and reaches the OS
	// OOM-kill point within ~3 minutes of leaving the normal working set, so the
	// guard samples often enough to detect a breach and shut down with minutes to
	// spare. runtime/metrics reads are cheap, so a tight interval is fine.
	memGuardInterval = 10 * time.Second

	// memGuardSustain is how long the footprint must stay above the limit before
	// the guard fires. Long enough that a single anomalous sample never triggers a
	// restart, short enough to win the race against a fast ramp: at ~10 GB/min the
	// guard trips a few GB above the limit, leaving ample runway under the
	// OOM-kill point for a graceful shutdown. The limit (well above the normal
	// working set) means any sustained breach is a real runaway, not reclaimable
	// garbage.
	memGuardSustain = 30 * time.Second

	// autoMemoryFraction is the share of total system RAM used as the limit when
	// no explicit limit is configured. Chosen to restart well before the machine
	// is under genuine memory pressure while leaving ample headroom over the
	// normal working set.
	autoMemoryFraction = 0.60
)

// ResolveMemoryLimit computes the guard's footprint ceiling in bytes from
// configuration. It returns 0 - meaning the guard is off - when disabled, or
// when auto mode cannot determine system RAM (systemRAM == 0), so an unknown
// environment never produces a dangerous guess. A positive limitMB always wins;
// otherwise the limit is autoMemoryFraction of total system RAM.
func ResolveMemoryLimit(enabled bool, limitMB int, systemRAM uint64) uint64 {
	if !enabled {
		return 0
	}
	if limitMB > 0 {
		return uint64(limitMB) * 1024 * 1024
	}
	if systemRAM == 0 {
		return 0
	}
	return uint64(float64(systemRAM) * autoMemoryFraction)
}

// MemoryGuard watches the process's Go-runtime memory footprint and fires
// onExceeded once when it stays above limit for a sustained window.
type MemoryGuard struct {
	limit      uint64
	sustain    time.Duration
	interval   time.Duration
	sample     func() uint64
	onExceeded func(footprint, limit uint64)

	// overSince is when the current continuous breach began, or the zero time
	// when the footprint is at or below the limit. Accessed only from Run's
	// single goroutine (and directly in tests).
	overSince time.Time
}

// NewMemoryGuard builds a guard that calls onExceeded once when the Go-runtime
// memory footprint stays above limitBytes for the sustained window. limitBytes
// must be > 0 (use ResolveMemoryLimit to decide whether to construct one).
func NewMemoryGuard(limitBytes uint64, onExceeded func(footprint, limit uint64)) *MemoryGuard {
	return &MemoryGuard{
		limit:      limitBytes,
		sustain:    memGuardSustain,
		interval:   memGuardInterval,
		sample:     goFootprintBytes,
		onExceeded: onExceeded,
	}
}

// Run samples memory on the guard's interval until ctx is cancelled, invoking
// onExceeded at most once (after which Run returns).
func (g *MemoryGuard) Run(ctx context.Context) {
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			footprint := g.sample()
			if g.breached(footprint, time.Now()) {
				g.onExceeded(footprint, g.limit)
				return
			}
		}
	}
}

// breached reports whether footprint has stayed above the limit continuously
// for at least the sustain window, tracking the start of the current breach. It
// is pure over (state, inputs) so the trip logic is unit-testable without
// timers.
func (g *MemoryGuard) breached(footprint uint64, now time.Time) bool {
	if footprint <= g.limit {
		g.overSince = time.Time{}
		return false
	}
	if g.overSince.IsZero() {
		g.overSince = now
	}
	return now.Sub(g.overSince) >= g.sustain
}

// goFootprintBytes returns the bytes of memory the Go runtime currently holds
// from the OS and has not released back (total mapped minus released). This
// tracks a heap leak's growth even when the OS later compresses or swaps the
// pages, and uses runtime/metrics to avoid the stop-the-world pause of
// runtime.ReadMemStats.
func goFootprintBytes() uint64 {
	samples := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(samples)
	total := samples[0].Value.Uint64()
	released := samples[1].Value.Uint64()
	if released > total {
		return 0
	}
	return total - released
}
