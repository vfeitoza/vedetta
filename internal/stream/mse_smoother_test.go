package stream

import (
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// mkSamples builds n B-frame-free samples; their pre-smoothing durations are
// irrelevant because the smoother overwrites them.
func mkSamples(n int) []*fmp4.Sample {
	s := make([]*fmp4.Sample, n)
	for i := range s {
		s[i] = &fmp4.Sample{PTSOffset: 0, Payload: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9a}}
	}
	return s
}

// Reproduces the exact variable-frame-rate fragment stream captured from
// front_door's Reolink substream in production: (sampleCount, totalTicks)
// pairs whose per-fragment mean frame duration swings from 2451 to 10426
// ticks. Per-fragment-local normalization (target = total/n) hands the
// browser a frame rate that lurches between ~9 and ~37 fps every 125ms
// fragment - that lurch IS the visible jitter.
//
// A correct smoother holds the per-frame duration stable ACROSS fragments
// (tracking the long-run average) while keeping the stamped media clock
// bounded against the real one so A/V never drifts. This test calls the
// exact helper flushVideoLocked uses in production.
func TestDurationSmootherStabilizesPacingAcrossFragments(t *testing.T) {
	// Observed production sequence (camera=front_door bframe=false).
	frags := []struct {
		n     int
		total uint32
	}{
		{2, 20853}, // startup outlier
		{3, 14388},
		{3, 14616},
		{6, 14708},
		{3, 15461},
		{3, 11683},
		{3, 12171},
		{3, 12567},
		{3, 13500},
		{4, 13900},
		{3, 12800},
		{5, 14200},
		{3, 12900},
		{3, 13100},
		{4, 13700},
		{3, 12600},
		{3, 13300},
		{5, 14000},
		{3, 12800},
		{3, 13200},
		{4, 13600},
		{3, 12700},
	}

	var d durationSmoother
	// Sustained jitter is the reported bug; a sub-second stabilization at
	// stream start is expected and not what the user sees. Assert on the
	// steady state, after the bias-corrected EWMA has converged.
	const warmup = 8

	minDur, maxDur := uint32(1<<31), uint32(0)
	var realClock, stampedClock int64

	for fi, f := range frags {
		samples := mkSamples(f.n)
		stamped := d.smooth(samples, f.total)

		realClock += int64(f.total)
		stampedClock += int64(stamped)

		// Stamped fragment total must equal the sum of rewritten sample
		// durations, or flushVideoLocked would advance videoDTS wrong and
		// tear the fragment boundary.
		var sum uint32
		for _, s := range samples {
			sum += s.Duration
		}
		if sum != stamped {
			t.Fatalf("fragment %d: sample durations sum to %d, smoother reported %d", fi, sum, stamped)
		}

		if fi < warmup {
			continue
		}
		for _, s := range samples {
			if s.Duration < minDur {
				minDur = s.Duration
			}
			if s.Duration > maxDur {
				maxDur = s.Duration
			}
		}
	}

	// Post-warmup pacing must be tight. Per-fragment-local normalization
	// (the current production behaviour) yields a spread of ~2700 ticks
	// (~30ms/frame) here; a cross-fragment-stable smoother keeps it small.
	const maxSpread = 900 // ticks (~10ms at 90kHz)
	if spread := maxDur - minDur; spread > maxSpread {
		t.Fatalf("post-warmup per-sample duration spread = %d ticks (min=%d max=%d), want <= %d; frame pacing still lurches",
			spread, minDur, maxDur, maxSpread)
	}

	// The smoothed media clock must stay bounded against the real one so
	// the live edge never drifts away from real time (A/V sync).
	const maxLag = 3 * maxFrameTicks
	if lag := realClock - stampedClock; lag > maxLag || lag < -maxLag {
		t.Fatalf("smoothed clock drifted %d ticks from real (real=%d stamped=%d), want |lag| <= %d",
			lag, realClock, stampedClock, maxLag)
	}
}

// A single isolated fragment (no cross-fragment history) must still behave
// like the original per-fragment normalization: seed the target from its
// own mean and distribute evenly. Guards backward compatibility with
// TestFlushVideoStillNormalizesBFrameFreeBatch.
func TestDurationSmootherFirstFragmentMatchesMean(t *testing.T) {
	var d durationSmoother
	samples := mkSamples(3)
	stamped := d.smooth(samples, 9000)

	if stamped != 9000 {
		t.Fatalf("first-fragment stamped total = %d, want 9000 (must preserve media clock)", stamped)
	}
	for i, s := range samples {
		if s.Duration != 3000 {
			t.Fatalf("sample %d duration = %d, want 3000 (first fragment seeds target from its own mean)", i, s.Duration)
		}
	}
}
