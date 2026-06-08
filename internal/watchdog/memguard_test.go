package watchdog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestResolveMemoryLimit_Disabled proves the guard is off (limit 0) whenever it
// is disabled, regardless of any explicit value or system RAM.
func TestResolveMemoryLimit_Disabled(t *testing.T) {
	if got := ResolveMemoryLimit(false, 4096, 16<<30); got != 0 {
		t.Fatalf("disabled guard resolved to %d, want 0", got)
	}
}

// TestResolveMemoryLimit_ExplicitWins proves a positive limitMB is used verbatim
// and never overridden by the auto fraction.
func TestResolveMemoryLimit_ExplicitWins(t *testing.T) {
	const limitMB = 6144
	want := uint64(limitMB) * 1024 * 1024
	if got := ResolveMemoryLimit(true, limitMB, 16<<30); got != want {
		t.Fatalf("explicit limit resolved to %d, want %d", got, want)
	}
}

// TestResolveMemoryLimit_AutoFraction proves limitMB==0 yields autoMemoryFraction
// of total system RAM.
func TestResolveMemoryLimit_AutoFraction(t *testing.T) {
	var ram uint64 = 16 << 30
	want := uint64(float64(ram) * autoMemoryFraction)
	if got := ResolveMemoryLimit(true, 0, ram); got != want {
		t.Fatalf("auto limit resolved to %d, want %d (%.0f%% of %d)", got, want, autoMemoryFraction*100, ram)
	}
}

// TestResolveMemoryLimit_AutoWithoutRAM proves auto mode degrades to disabled
// (0) when system RAM cannot be determined, rather than guessing a dangerous
// value.
func TestResolveMemoryLimit_AutoWithoutRAM(t *testing.T) {
	if got := ResolveMemoryLimit(true, 0, 0); got != 0 {
		t.Fatalf("auto limit with unknown RAM resolved to %d, want 0", got)
	}
}

// TestMemoryGuard_breached_UnderLimitResets proves a footprint at or below the
// limit never trips and clears any in-progress breach window.
func TestMemoryGuard_breached_UnderLimitResets(t *testing.T) {
	g := &MemoryGuard{limit: 1000, sustain: time.Minute}
	base := time.Unix(0, 0)

	// Enter a breach, then drop back under: the window must reset.
	if g.breached(1500, base) {
		t.Fatal("breached fired immediately on first over-limit sample")
	}
	if g.breached(900, base.Add(2*time.Minute)) {
		t.Fatal("breached fired while under the limit")
	}
	if !g.overSince.IsZero() {
		t.Fatal("breach window was not reset after dropping under the limit")
	}
}

// TestMemoryGuard_breached_TransientSpikeDoesNotTrip proves a brief spike over
// the limit that recovers before the sustain window elapses never trips - the
// signature of reclaimable garbage, not a leak.
func TestMemoryGuard_breached_TransientSpikeDoesNotTrip(t *testing.T) {
	g := &MemoryGuard{limit: 1000, sustain: time.Minute}
	base := time.Unix(0, 0)

	if g.breached(2000, base) {
		t.Fatal("tripped on the first over-limit sample, before the sustain window")
	}
	if g.breached(2000, base.Add(30*time.Second)) {
		t.Fatal("tripped at 30s, before the 60s sustain window elapsed")
	}
	if g.breached(500, base.Add(40*time.Second)) {
		t.Fatal("tripped after recovering under the limit")
	}
}

// TestMemoryGuard_breached_SustainedBreachTrips proves a footprint that stays
// over the limit for at least the sustain window trips exactly when the window
// elapses.
func TestMemoryGuard_breached_SustainedBreachTrips(t *testing.T) {
	g := &MemoryGuard{limit: 1000, sustain: time.Minute}
	base := time.Unix(0, 0)

	if g.breached(2000, base) {
		t.Fatal("tripped on the first over-limit sample")
	}
	if g.breached(2000, base.Add(59*time.Second)) {
		t.Fatal("tripped one second before the sustain window elapsed")
	}
	if !g.breached(2000, base.Add(60*time.Second)) {
		t.Fatal("did not trip once the sustain window had elapsed under a continuous breach")
	}
}

// TestMemoryGuard_breached_TripsWithRunwayOnFastRamp proves the production
// cadence wins the race against the observed runaway: a heap ramping ~10 GB/min
// must trip the guard while the footprint still leaves ample runway below the
// ~36 GB point where macOS jetsam OOM-kills the process. It drives breached()
// with the real memGuardInterval / memGuardSustain constants, so loosening the
// cadence back toward a slow leak (which would lose the race) fails this test.
func TestMemoryGuard_breached_TripsWithRunwayOnFastRamp(t *testing.T) {
	const gib = float64(uint64(1) << 30)
	g := &MemoryGuard{limit: 9 * (uint64(1) << 30), sustain: memGuardSustain}
	base := time.Unix(0, 0)

	tripGB := 0.0
	for i := 0; i < 1000; i++ {
		elapsedSec := float64(i) * memGuardInterval.Seconds()
		footGB := 0.6 + (10.0/60.0)*elapsedSec // ramp from 0.6 GB at 10 GB/min
		now := base.Add(time.Duration(i) * memGuardInterval)
		if g.breached(uint64(footGB*gib), now) {
			tripGB = footGB
			break
		}
	}

	if tripGB == 0 {
		t.Fatal("guard never tripped on a sustained 10 GB/min ramp")
	}
	if tripGB > 20 {
		t.Fatalf("guard tripped at %.1f GB; too late - cadence (interval=%s, sustain=%s) loses the race to the ~36 GB OOM point",
			tripGB, memGuardInterval, memGuardSustain)
	}
}

// TestMemoryGuard_Run_FiresOnceOnSustainedBreach proves Run samples on its
// interval and invokes onExceeded exactly once when the footprint stays high.
func TestMemoryGuard_Run_FiresOnceOnSustainedBreach(t *testing.T) {
	var fired int32
	var gotFootprint, gotLimit uint64
	g := &MemoryGuard{
		limit:    1000,
		sustain:  20 * time.Millisecond,
		interval: 5 * time.Millisecond,
		sample:   func() uint64 { return 5000 },
		onExceeded: func(footprint, limit uint64) {
			atomic.AddInt32(&fired, 1)
			gotFootprint, gotLimit = footprint, limit
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { g.Run(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after firing on a sustained breach")
	}

	if n := atomic.LoadInt32(&fired); n != 1 {
		t.Fatalf("onExceeded fired %d times, want exactly 1", n)
	}
	if gotFootprint != 5000 || gotLimit != 1000 {
		t.Fatalf("onExceeded got footprint=%d limit=%d, want 5000/1000", gotFootprint, gotLimit)
	}
}

// TestMemoryGuard_Run_SilentUnderLimit proves a healthy process is never
// restarted: while the footprint stays under the limit, onExceeded never fires.
func TestMemoryGuard_Run_SilentUnderLimit(t *testing.T) {
	var fired int32
	g := &MemoryGuard{
		limit:      1000,
		sustain:    10 * time.Millisecond,
		interval:   2 * time.Millisecond,
		sample:     func() uint64 { return 100 },
		onExceeded: func(uint64, uint64) { atomic.AddInt32(&fired, 1) },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	g.Run(ctx)

	if n := atomic.LoadInt32(&fired); n != 0 {
		t.Fatalf("onExceeded fired %d times while under the limit", n)
	}
}

// TestGoFootprintBytes_Positive proves the default sampler reports a sane,
// non-zero footprint: the Go runtime always holds some memory from the OS.
func TestGoFootprintBytes_Positive(t *testing.T) {
	if got := goFootprintBytes(); got == 0 {
		t.Fatal("goFootprintBytes returned 0; expected a positive runtime footprint")
	}
}

// TestSystemMemoryBytes_Positive proves the platform RAM probe returns a
// plausible total on the host running the tests.
func TestSystemMemoryBytes_Positive(t *testing.T) {
	got, err := SystemMemoryBytes()
	if err != nil {
		t.Fatalf("SystemMemoryBytes failed: %v", err)
	}
	if got < 256<<20 {
		t.Fatalf("SystemMemoryBytes returned %d bytes, implausibly small for a test host", got)
	}
}
