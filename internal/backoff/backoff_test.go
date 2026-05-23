package backoff

import (
	"testing"
	"time"
)

func TestJitter_WithinHalfToFullRange(t *testing.T) {
	const d = 10 * time.Second
	// frac spans [0,1); the result must stay within [d/2, d).
	for _, frac := range []float64{0, 0.01, 0.25, 0.5, 0.75, 0.999} {
		got := Jitter(d, frac)
		if got < d/2 || got >= d {
			t.Errorf("Jitter(%v, %v) = %v, want within [%v, %v)", d, frac, got, d/2, d)
		}
	}
}

func TestJitter_FracEndpoints(t *testing.T) {
	const d = 8 * time.Second
	if got := Jitter(d, 0); got != d/2 {
		t.Errorf("Jitter at frac=0 = %v, want %v (half)", got, d/2)
	}
	// frac approaching 1 approaches the full duration but never reaches it.
	got := Jitter(d, 0.999)
	if got <= d*3/4 || got >= d {
		t.Errorf("Jitter at frac~1 = %v, want close to %v but < %v", got, d, d)
	}
}

func TestJitter_NonPositiveUnchanged(t *testing.T) {
	if got := Jitter(0, 0.5); got != 0 {
		t.Errorf("Jitter(0,...) = %v, want 0", got)
	}
	if got := Jitter(-time.Second, 0.5); got != -time.Second {
		t.Errorf("Jitter(negative,...) = %v, want unchanged", got)
	}
}

func TestJitter_Varies(t *testing.T) {
	const d = time.Minute
	a := Jitter(d, 0.1)
	b := Jitter(d, 0.9)
	if a == b {
		t.Errorf("expected different jittered values for different fracs, both %v", a)
	}
}
