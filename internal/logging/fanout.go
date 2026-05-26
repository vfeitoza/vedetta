package logging

import (
	"context"
	"errors"
	"log/slog"
	"slices"
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

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := make([]slog.Handler, len(f.arms))
	for i, h := range f.arms {
		// Each arm owns its attrs slice per the slog contract; give each its own copy.
		derived[i] = h.WithAttrs(slices.Clone(attrs))
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
