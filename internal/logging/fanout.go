package logging

import (
	"context"
	"errors"
	"log/slog"
)

// fanoutHandler broadcasts every record to all arms. It is used to tee logs to
// the local stdout handler and the OTLP arm simultaneously. A failure in one
// arm never suppresses delivery to the others.
type fanoutHandler struct {
	arms []slog.Handler
}

func newFanout(arms ...slog.Handler) *fanoutHandler {
	return &fanoutHandler{arms: arms}
}

func (f *fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.arms {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs error
	for _, h := range f.arms {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		// Hand each arm its own Clone. This is the idiomatic, contract-safe way
		// to deliver a record to multiple independent handlers; it isolates any
		// arm that mutates (e.g. AddAttrs) or retains the record.
		if err := h.Handle(ctx, r.Clone()); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

// cloneAttrs returns a deep copy of attrs so each fanout arm owns its slice and
// any nested group backing arrays. slog's WithAttrs contract grants the handler
// ownership of the slice it receives; recursing into groups keeps that ownership
// true per arm even when an arm rewrites grouped attrs in place. Only group
// values are recursed; scalar/immutable values are copied by value.
func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	if attrs == nil {
		return nil
	}
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		if a.Value.Kind() == slog.KindGroup {
			a.Value = slog.GroupValue(cloneAttrs(a.Value.Group())...)
		}
		out[i] = a
	}
	return out
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := make([]slog.Handler, len(f.arms))
	for i, h := range f.arms {
		// Each arm owns its attrs (incl. nested groups) per the slog contract.
		derived[i] = h.WithAttrs(cloneAttrs(attrs))
	}
	return &fanoutHandler{arms: derived}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	derived := make([]slog.Handler, len(f.arms))
	for i, h := range f.arms {
		derived[i] = h.WithGroup(name)
	}
	return &fanoutHandler{arms: derived}
}
