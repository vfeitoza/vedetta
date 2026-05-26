package logging

import (
	"context"
	"log/slog"
	"testing"
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
