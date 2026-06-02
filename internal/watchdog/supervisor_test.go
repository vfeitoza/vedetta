package watchdog

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// runSupervisor must SIGKILL the parent only when the heartbeat truly stops.
// These tests drive the exact production decision function over a real pipe.

// TestMain lets this test binary re-exec itself as the supervisor child, so
// SuperviseSelf (which spawns os.Executable()) can be exercised end to end.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == SupervisorArg {
		os.Exit(RunSupervisorChild())
	}
	os.Exit(m.Run())
}

func TestSupervisorKillsWhenHeartbeatStops(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer w.Close() // keep the write end open so the reader times out, not EOFs
	defer r.Close()

	var killed atomic.Int32
	start := time.Now()
	runSupervisor(r, 40*time.Millisecond, func() { killed.Add(1) })

	if killed.Load() != 1 {
		t.Fatalf("expected exactly one kill, got %d", killed.Load())
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("killed too early after %v, want >= timeout", elapsed)
	}
}

func TestSupervisorExitsOnParentGoneWithoutKill(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	w.Close() // parent closed the write end (exited cleanly) -> reader sees EOF

	var killed atomic.Int32
	runSupervisor(r, time.Second, func() { killed.Add(1) })

	if killed.Load() != 0 {
		t.Fatalf("must not kill on a clean parent exit (EOF), got %d kills", killed.Load())
	}
}

func TestSupervisorStaysAliveWhileHeartbeating(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = heartbeatLoop(ctx, w, 5*time.Millisecond) }()

	var killed atomic.Int32
	done := make(chan struct{})
	go func() {
		runSupervisor(r, 40*time.Millisecond, func() { killed.Add(1) })
		close(done)
	}()

	// Heartbeats every 5ms for 150ms must keep the supervisor from firing,
	// even though the timeout is only 40ms.
	time.Sleep(150 * time.Millisecond)
	if killed.Load() != 0 {
		t.Fatalf("killed despite steady heartbeats, got %d kills", killed.Load())
	}

	// Simulate clean shutdown: parent closes the pipe -> supervisor exits, no kill.
	cancel()
	w.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not exit after pipe close")
	}
	if killed.Load() != 0 {
		t.Fatalf("killed on clean shutdown, got %d kills", killed.Load())
	}
}

// TestSuperviseSelfSpawnsChildAndStopsCleanly exercises the real subprocess
// path: SuperviseSelf spawns this test binary as the supervisor child, streams
// it heartbeats, and stop() must cancel the heartbeat, let the child exit on
// pipe EOF, and reap it. A generous timeout ensures the child never kills the
// test process; if the child were not spawned or did not exit on EOF, stop()
// would block and the guard below would fail.
func TestSuperviseSelfSpawnsChildAndStopsCleanly(t *testing.T) {
	stop := SuperviseSelf(context.Background(), 20*time.Millisecond, 30*time.Second)

	done := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond) // let the child spawn and read heartbeats
		stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SuperviseSelf did not spawn/teardown the supervisor child cleanly")
	}
}
