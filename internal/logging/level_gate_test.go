package logging

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestLevelGate(t *testing.T) {
	// inner accepts everything at/above Debug.
	inner := slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelDebug})
	g := newLevelGate(slog.LevelInfo, inner)

	if g.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("levelGate must reject Debug below its Info minimum")
	}
	if !g.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("levelGate must accept Info at its minimum")
	}
	if !g.Enabled(context.Background(), slog.LevelError) {
		t.Error("levelGate must accept Error above its minimum")
	}
}

func TestLevelGateRespectsInner(t *testing.T) {
	// inner rejects below Error; gate min is Info. Info must be rejected by inner.
	inner := slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError})
	g := newLevelGate(slog.LevelInfo, inner)
	if g.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("levelGate must defer to inner.Enabled when above its own minimum")
	}
}

// discard is an io.Writer sink for handler construction in tests.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// recordingHandler counts Handle calls and is Enabled at every level, so tests
// exercise the gate's own filtering rather than an inner handler's level.
type recordingHandler struct{ count int }

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h *recordingHandler) Handle(context.Context, slog.Record) error { h.count++; return nil }
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler             { return h }

func TestLevelGateHandleDropsSubThreshold(t *testing.T) {
	inner := &recordingHandler{}
	g := newLevelGate(slog.LevelInfo, inner)

	// A fan-out can broadcast a sub-threshold record straight to Handle,
	// bypassing Enabled (another arm enabled it). The gate must still drop it.
	debug := slog.NewRecord(time.Now(), slog.LevelDebug, "debug", 0)
	if err := g.Handle(context.Background(), debug); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if inner.count != 0 {
		t.Errorf("levelGate.Handle must drop sub-threshold records; inner saw %d", inner.count)
	}

	// At/above the minimum, the record must reach the inner handler.
	info := slog.NewRecord(time.Now(), slog.LevelInfo, "info", 0)
	if err := g.Handle(context.Background(), info); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if inner.count != 1 {
		t.Errorf("levelGate.Handle must forward at-threshold records; inner saw %d", inner.count)
	}
}
