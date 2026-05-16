package watchdog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchdog_FiresWhenNotKicked proves the watchdog invokes its timeout
// action when no heartbeat arrives within the deadline. In production the
// action is os.Exit, which lets launchd KeepAlive restart a hung process
// instead of leaving it grey-failed indefinitely.
func TestWatchdog_FiresWhenNotKicked(t *testing.T) {
	var fired int32
	w := New(40*time.Millisecond, func(time.Duration) {
		atomic.AddInt32(&fired, 1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&fired) == 0 {
		select {
		case <-deadline:
			t.Fatal("watchdog did not fire after the timeout elapsed without a kick")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestWatchdog_DoesNotFireWhileKicked proves a regularly-kicked watchdog stays
// silent: a healthy process must never be killed.
func TestWatchdog_DoesNotFireWhileKicked(t *testing.T) {
	var fired int32
	w := New(80*time.Millisecond, func(time.Duration) {
		atomic.AddInt32(&fired, 1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)

	stop := time.After(400 * time.Millisecond)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-ticker.C:
			w.Kick()
		}
	}
	cancel()

	if n := atomic.LoadInt32(&fired); n != 0 {
		t.Fatalf("watchdog fired %d times while being kicked regularly", n)
	}
}

// TestWatchdog_StopsOnContextCancel proves Run returns when the context is
// cancelled and does not fire afterwards.
func TestWatchdog_StopsOnContextCancel(t *testing.T) {
	var fired int32
	w := New(30*time.Millisecond, func(time.Duration) {
		atomic.AddInt32(&fired, 1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	time.Sleep(80 * time.Millisecond)
	if n := atomic.LoadInt32(&fired); n != 0 {
		t.Fatalf("watchdog fired %d times after being stopped", n)
	}
}
