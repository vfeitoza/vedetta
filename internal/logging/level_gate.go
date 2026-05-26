package logging

import (
	"context"
	"log/slog"
)

// levelGate wraps a slog.Handler and enforces a minimum level on top of
// whatever the inner handler already allows. It exists so the OTLP arm cannot
// report Enabled for sub-threshold records (which would otherwise force record
// construction and export below the intended level).
type levelGate struct {
	min   slog.Level
	inner slog.Handler
}

func newLevelGate(min slog.Level, inner slog.Handler) *levelGate {
	return &levelGate{min: min, inner: inner}
}

func (g *levelGate) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= g.min && g.inner.Enabled(ctx, l)
}

func (g *levelGate) Handle(ctx context.Context, r slog.Record) error {
	if !g.Enabled(ctx, r.Level) {
		return nil
	}
	return g.inner.Handle(ctx, r)
}

func (g *levelGate) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelGate{min: g.min, inner: g.inner.WithAttrs(attrs)}
}

func (g *levelGate) WithGroup(name string) slog.Handler {
	return &levelGate{min: g.min, inner: g.inner.WithGroup(name)}
}
