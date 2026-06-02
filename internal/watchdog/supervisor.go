package watchdog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// The out-of-process supervisor closes the gap the in-process Watchdog cannot:
// when the Go runtime wedges (heap corruption stalls the scheduler), no
// goroutine - including the Watchdog's os.Exit - ever runs again, so the
// process spins forever holding its sockets and the supervisor (launchd /
// container runtime) never restarts it. A separate child process has its own
// runtime, so it survives the parent's wedge and force-kills it.
const (
	// SupervisorHeartbeatInterval is how often the main process proves to the
	// child that its scheduler is still alive.
	SupervisorHeartbeatInterval = 10 * time.Second

	// SupervisorTimeout is how long the child waits without a heartbeat before
	// force-killing the parent. Generous relative to the interval so transient
	// pauses (GC, disk) never trip it - only a frozen runtime stops heartbeats
	// for this long.
	SupervisorTimeout = 60 * time.Second

	// SupervisorArg is the hidden subcommand that runs the supervisor child.
	SupervisorArg = "__supervise__"

	envSupervisePPID    = "VEDETTA_SUPERVISE_PPID"
	envSuperviseTimeout = "VEDETTA_SUPERVISE_TIMEOUT_MS"

	// heartbeatFD is the inherited pipe read end in the child. 0/1/2 are
	// stdin/stdout/stderr; ExtraFiles[0] lands on fd 3.
	heartbeatFD = 3

	heartbeatByte = byte(1)
)

// runSupervisor blocks reading heartbeats from r. It calls kill exactly once if
// no heartbeat arrives within timeout (the parent's runtime is wedged), then
// returns. It returns WITHOUT calling kill when r reports EOF - the parent
// closed the pipe by exiting or shutting down cleanly. That EOF rule makes it
// PID-reuse safe: a live-but-wedged parent holds the pipe open with no data, a
// dead parent's pipe is closed, so a stale PID is never killed.
//
// The read runs on a blocking goroutine and the timeout on a timer, rather than
// SetReadDeadline: the heartbeat pipe is inherited by the child as a plain fd
// that does not support deadlines ("file type does not support deadline"), so a
// deadline-based read would fail immediately.
func runSupervisor(r io.Reader, timeout time.Duration, kill func()) {
	beats := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				select {
				case beats <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return // EOF or read error: parent gone
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-beats:
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(timeout)
		case <-done:
			return // parent closed the pipe (clean exit / shutdown): never kill
		case <-timer.C:
			// Guard against a parent that vanished at the same instant the timer
			// fired - a clean exit must never be treated as a wedge.
			select {
			case <-done:
			default:
				kill()
			}
			return
		}
	}
}

// heartbeatLoop writes a heartbeat byte to w every interval until ctx is
// cancelled or a write fails (the child has gone away). If the runtime wedges,
// this goroutine simply stops running, the heartbeats stop, and the child kills
// the parent - which is exactly the intended behaviour.
func heartbeatLoop(ctx context.Context, w io.Writer, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := w.Write([]byte{heartbeatByte}); err != nil {
				return err
			}
		}
	}
}

// RunSupervisorChild is the entry point for the SupervisorArg subcommand. It
// watches the inherited heartbeat pipe and SIGKILLs the parent if the parent's
// runtime wedges, so launchd / the container runtime restarts it.
func RunSupervisorChild() int {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	f := os.NewFile(heartbeatFD, "vedetta-heartbeat")
	if f == nil {
		slog.Error("supervisor: heartbeat pipe not inherited; exiting")
		return 2
	}
	defer f.Close()

	ppid, _ := strconv.Atoi(os.Getenv(envSupervisePPID))
	if ppid <= 1 {
		ppid = os.Getppid()
	}

	timeout := SupervisorTimeout
	if ms, err := strconv.Atoi(os.Getenv(envSuperviseTimeout)); err == nil && ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}

	runSupervisor(f, timeout, func() {
		slog.Error("supervisor: no heartbeat from main process within timeout, killing it for restart",
			"pid", ppid, "timeout", timeout)
		if err := syscall.Kill(ppid, syscall.SIGKILL); err != nil {
			slog.Error("supervisor: kill failed", "pid", ppid, "error", err)
		}
	})
	return 0
}

// SuperviseSelf spawns a copy of this binary as an out-of-process supervisor and
// streams it a heartbeat. The supervisor force-kills this process if the
// heartbeat stops - a runtime wedge the in-process Watchdog cannot escape. It
// respawns the supervisor if it dies and tears it down when ctx is cancelled or
// the returned stop func is called. On any spawn failure it logs and returns a
// no-op stop, so it never blocks startup.
func SuperviseSelf(parent context.Context, interval, timeout time.Duration) func() {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("supervisor disabled: cannot resolve executable", "error", err)
		return func() {}
	}

	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	wg.Go(func() {
		for ctx.Err() == nil {
			if err := superviseOnce(ctx, exe, interval, timeout); err != nil {
				slog.Warn("supervisor process ended, respawning", "error", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second): // backoff so a crash-looping child can't spin
			}
		}
	})

	return func() {
		cancel()
		wg.Wait()
	}
}

// superviseOnce spawns one supervisor child and feeds it heartbeats until the
// child dies or ctx is cancelled, then reaps it.
func superviseOnce(ctx context.Context, exe string, interval, timeout time.Duration) error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	defer w.Close()

	cmd := exec.Command(exe, SupervisorArg)
	cmd.ExtraFiles = []*os.File{r} // inherited as fd 3 in the child
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("%s=%d", envSupervisePPID, os.Getpid()),
		fmt.Sprintf("%s=%d", envSuperviseTimeout, timeout.Milliseconds()),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		r.Close()
		return fmt.Errorf("start supervisor: %w", err)
	}
	r.Close() // the child owns the read end now

	hbErr := heartbeatLoop(ctx, w, interval)
	w.Close()      // closing the write end signals EOF so the child exits cleanly
	_ = cmd.Wait() // reap the child
	return hbErr
}
