package logging

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// captureHandler is an immutable slog.Handler for tests. Handle appends to
// records; WithAttrs/WithGroup return a NEW child handler (modeling slog's
// immutability contract) and record the child on the parent so a test can
// inspect what derivation reached each arm. min sets the Enabled floor; err is
// returned from Handle.
type captureHandler struct {
	records  *[]slog.Record
	min      slog.Level
	err      error
	attrs    []slog.Attr // attrs this (child) handler was created with
	group    string      // group this (child) handler was created with
	children *[]*captureHandler
}

func newCapture(min slog.Level, err error) *captureHandler {
	return &captureHandler{records: &[]slog.Record{}, min: min, err: err, children: &[]*captureHandler{}}
}

func (c *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= c.min }
func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	*c.records = append(*c.records, r)
	return c.err
}
func (c *captureHandler) WithAttrs(a []slog.Attr) slog.Handler {
	child := &captureHandler{records: &[]slog.Record{}, min: c.min, err: c.err, attrs: a, children: &[]*captureHandler{}}
	*c.children = append(*c.children, child)
	return child
}
func (c *captureHandler) WithGroup(name string) slog.Handler {
	child := &captureHandler{records: &[]slog.Record{}, min: c.min, err: c.err, group: name, children: &[]*captureHandler{}}
	*c.children = append(*c.children, child)
	return child
}

// handlerFunc adapts a Handle func into a slog.Handler (Enabled always true,
// WithAttrs/WithGroup return self) so clone-isolation tests can mutate the
// record they receive.
type handlerFunc func(context.Context, slog.Record) error

func (h handlerFunc) Enabled(context.Context, slog.Level) bool        { return true }
func (h handlerFunc) Handle(ctx context.Context, r slog.Record) error { return h(ctx, r) }
func (h handlerFunc) WithAttrs([]slog.Attr) slog.Handler              { return h }
func (h handlerFunc) WithGroup(string) slog.Handler                   { return h }

func newRecord(level slog.Level, msg string) slog.Record {
	return slog.NewRecord(time.Now(), level, msg, 0)
}

func TestFanoutDeliversToAllArms(t *testing.T) {
	a, b := newCapture(slog.LevelDebug, nil), newCapture(slog.LevelDebug, nil)
	f := newFanout(a, b)
	if err := f.Handle(context.Background(), newRecord(slog.LevelInfo, "hi")); err != nil {
		t.Fatalf("Handle returned %v", err)
	}
	if len(*a.records) != 1 || len(*b.records) != 1 {
		t.Fatalf("both arms must receive the record: a=%d b=%d", len(*a.records), len(*b.records))
	}
}

func TestFanoutOneArmErrorDoesNotSuppressOther(t *testing.T) {
	failing := newCapture(slog.LevelDebug, errors.New("export down"))
	base := newCapture(slog.LevelDebug, nil)
	// failing arm first, so a naive implementation that returns early would skip base.
	f := newFanout(failing, base)
	err := f.Handle(context.Background(), newRecord(slog.LevelInfo, "hi"))
	if err == nil {
		t.Error("Handle must return the failing arm's error")
	}
	if len(*base.records) != 1 {
		t.Error("base arm must still receive the record despite the other arm failing")
	}
}

func TestFanoutEnabledIsOr(t *testing.T) {
	infoArm := newCapture(slog.LevelInfo, nil)
	errArm := newCapture(slog.LevelError, nil)
	f := newFanout(infoArm, errArm)
	if !f.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled must be true when any arm accepts Info")
	}
	// Both gated at Info or higher: Debug must be false.
	both := newFanout(newCapture(slog.LevelInfo, nil), newCapture(slog.LevelInfo, nil))
	if both.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Enabled(Debug) must be false when both arms are Info-gated")
	}
}

// TestFanoutDeliversRecordContentIntactToEachArm verifies every arm receives the
// record's full attribute set (not just the message), and that an arm appending
// its own attr still sees the complete picture.
func TestFanoutDeliversRecordContentIntactToEachArm(t *testing.T) {
	collect := func(seen *[]string) handlerFunc {
		return func(_ context.Context, r slog.Record) error {
			r.AddAttrs(slog.String("armkey", "armval"))
			r.Attrs(func(a slog.Attr) bool { *seen = append(*seen, a.Key); return true })
			return nil
		}
	}
	var aKeys, bKeys []string
	f := newFanout(collect(&aKeys), collect(&bKeys))

	rec := newRecord(slog.LevelInfo, "hi")
	rec.AddAttrs(slog.Int("a", 1), slog.Int("b", 2), slog.Int("c", 3),
		slog.Int("d", 4), slog.Int("e", 5), slog.Int("f", 6))

	if err := f.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle returned %v", err)
	}
	// Each arm must see the 6 base attrs plus its own single "armkey".
	if len(aKeys) != 7 || len(bKeys) != 7 {
		t.Fatalf("each arm must observe all 7 attrs: a=%d b=%d", len(aKeys), len(bKeys))
	}
	if countKey(aKeys, "armkey") != 1 || countKey(bKeys, "armkey") != 1 {
		t.Errorf("each arm must see exactly one armkey: a=%d b=%d",
			countKey(aKeys, "armkey"), countKey(bKeys, "armkey"))
	}
}

func countKey(keys []string, want string) int {
	n := 0
	for _, k := range keys {
		if k == want {
			n++
		}
	}
	return n
}

// TestFanoutClonesRecordForRetainedCopies is the regression test for the per-arm
// r.Clone() in Handle. It fails if Clone is removed. The arms retain the records
// they receive and append to them after Handle returns. slog.Record carries an
// unsafe-copy guard: appending to two copies that share a backing array with
// spare capacity makes the second AddAttrs inject a "!BUG ... without using
// Record.Clone" attr. The base record is grown one AddAttrs at a time so its
// overflow attr slice has spare capacity (the precondition for the guard to
// fire); without per-arm Clone the two retained records share that slice.
//
// This is a white-box test that relies on slog's current overflow-slice guard
// (5 inline front attrs, Record.Clone clipping the overflow slice, and AddAttrs
// detecting an already-used spare slot). If a future Go release changes those
// internals the precondition may stop holding; the assertion (no "!BUG" attr)
// stays correct regardless.
func TestFanoutClonesRecordForRetainedCopies(t *testing.T) {
	var ra, rb slog.Record
	f := newFanout(
		handlerFunc(func(_ context.Context, r slog.Record) error { ra = r; return nil }),
		handlerFunc(func(_ context.Context, r slog.Record) error { rb = r; return nil }),
	)

	rec := newRecord(slog.LevelInfo, "hi")
	rec.AddAttrs(slog.String("f1", "1"), slog.String("f2", "2"), slog.String("f3", "3"),
		slog.String("f4", "4"), slog.String("f5", "5")) // fill the inline front
	rec.AddAttrs(slog.String("b1", "1")) // grow the overflow slice incrementally
	rec.AddAttrs(slog.String("b2", "2"))
	rec.AddAttrs(slog.String("b3", "3")) // now the overflow slice has spare capacity

	if err := f.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle returned %v", err)
	}
	ra.AddAttrs(slog.String("armA", "x"))
	rb.AddAttrs(slog.String("armB", "y"))

	if hasUnsafeCopyBug(ra) || hasUnsafeCopyBug(rb) {
		t.Error("fanout must hand each arm its own r.Clone(); retained copies shared a backing array")
	}
}

func hasUnsafeCopyBug(r slog.Record) bool {
	bug := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "!BUG" {
			bug = true
			return false
		}
		return true
	})
	return bug
}

func TestFanoutWithAttrsAndGroupPropagateAndDoNotMutateReceiver(t *testing.T) {
	a, b := newCapture(slog.LevelDebug, nil), newCapture(slog.LevelDebug, nil)
	f := newFanout(a, b)
	derived := f.WithAttrs([]slog.Attr{slog.String("k", "v")}).WithGroup("g")

	// Each original arm must have produced one WithAttrs child then one WithGroup
	// grandchild, proving derivation reached both arms.
	if len(*a.children) != 1 || len(*b.children) != 1 {
		t.Fatalf("WithAttrs must derive a child on both arms: a=%d b=%d", len(*a.children), len(*b.children))
	}
	aAttrChild, bAttrChild := (*a.children)[0], (*b.children)[0]
	if len(aAttrChild.attrs) != 1 || len(bAttrChild.attrs) != 1 {
		t.Errorf("WithAttrs child must carry the attr on both arms")
	}
	if len(*aAttrChild.children) != 1 || (*aAttrChild.children)[0].group != "g" {
		t.Errorf("WithGroup must derive a grandchild named g on arm a")
	}
	if len(*bAttrChild.children) != 1 || (*bAttrChild.children)[0].group != "g" {
		t.Errorf("WithGroup must derive a grandchild named g on arm b")
	}

	// Handling through the derived fanout reaches the grandchildren, not the
	// original arms.
	_ = derived.Handle(context.Background(), newRecord(slog.LevelInfo, "hi"))
	if len(*a.records) != 0 || len(*b.records) != 0 {
		t.Error("derived fanout must not deliver to the original arms")
	}

	// The original fanout is unchanged: emitting through it still hits a and b.
	if err := f.Handle(context.Background(), newRecord(slog.LevelInfo, "again")); err != nil {
		t.Fatalf("Handle returned %v", err)
	}
	if len(*a.records) != 1 || len(*b.records) != 1 {
		t.Error("original fanout must remain functional and unmodified")
	}
}

// attrCaptureFunc is a slog.Handler whose WithAttrs runs a callback on the
// slice it is handed (the slog contract says the handler owns that slice), then
// returns itself. It lets a test prove the fanout gives each arm an independent
// attrs slice.
type attrCaptureFunc func([]slog.Attr)

func (attrCaptureFunc) Enabled(context.Context, slog.Level) bool  { return true }
func (attrCaptureFunc) Handle(context.Context, slog.Record) error { return nil }
func (f attrCaptureFunc) WithAttrs(a []slog.Attr) slog.Handler    { f(a); return f }
func (f attrCaptureFunc) WithGroup(string) slog.Handler           { return f }

func TestFanoutWithAttrsGivesEachArmIndependentSlice(t *testing.T) {
	var seenByObserver []slog.Attr
	// arm 0 mutates the slice it owns (allowed by the contract): clobbers elem 0.
	mutator := attrCaptureFunc(func(a []slog.Attr) {
		if len(a) > 0 {
			a[0] = slog.String("clobbered", "yes")
		}
	})
	// arm 1 records what it was handed; it must not see the mutator's clobber.
	observer := attrCaptureFunc(func(a []slog.Attr) { seenByObserver = a })

	f := newFanout(mutator, observer)
	attrs := []slog.Attr{slog.String("orig", "val")}
	_ = f.WithAttrs(attrs)

	if len(seenByObserver) != 1 || seenByObserver[0].Key != "orig" {
		t.Errorf("observer arm must see its own copy, got %v", seenByObserver)
	}
	if attrs[0].Key != "orig" {
		t.Errorf("caller's attrs slice must not be mutated, got %v", attrs)
	}
}
