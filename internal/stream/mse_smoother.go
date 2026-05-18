package stream

import "github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"

// durationSmoother rewrites B-frame-free fMP4 sample durations so the browser
// renders frames at an even cadence even when the camera delivers them at a
// highly irregular rate.
//
// Per-fragment-local normalization (target = fragmentTotal / sampleCount,
// recomputed each ~125ms flush) only evens jitter WITHIN a fragment: because
// a variable-frame-rate camera makes both the sample count and the accumulated
// total swing fragment-to-fragment, the per-frame duration handed to the
// browser lurches every fragment (observed: 2451..10426 ticks, ~9..37 fps).
// That lurch is the visible jitter.
//
// Instead the smoother tracks the long-run average frame duration as an EWMA
// held ACROSS fragments and stamps every sample with that stable target. The
// difference between this smoothed clock and the true media clock is bled off
// a small bounded amount per fragment, so pacing stays even while the live
// edge never drifts away from real time (A/V sync preserved). The zero value
// is ready to use; the first fragment seeds the EWMA from its own mean.
type durationSmoother struct {
	ewma        float64 // ticks per frame; 0 until the first fragment seeds it
	count       int64   // fragments seen, for bias-corrected warmup
	smoothClock int64   // total ticks stamped so far
	realClock   int64   // total real ticks observed so far
}

const (
	// EWMA weight for each fragment's mean. Small enough that a startup or
	// glitch fragment cannot yank the target, large enough to track a
	// genuine rate change within a couple of seconds.
	smoothAlpha = 0.1
	// Sane bounds (90kHz) so an absurd fragment can't peg the target.
	minFrameTicks = 90000 / 60 // 60 fps
	maxFrameTicks = 90000 / 2  // 2 fps
)

// smooth rewrites every sample's Duration to a cross-fragment-stable target
// and returns the stamped fragment total (the sum of the rewritten
// durations). Callers MUST advance their decode clock by the returned value,
// not by the real input total, or the fragment boundary tears. samples must
// be non-empty; totalTicks is the real summed DTS duration of the fragment.
func (d *durationSmoother) smooth(samples []*fmp4.Sample, totalTicks uint32) uint32 {
	n := len(samples)
	if n == 0 {
		return 0
	}

	mean := float64(totalTicks) / float64(n)
	d.count++
	if d.ewma == 0 {
		d.ewma = mean
	} else {
		// Bias-corrected warmup: behave like a running mean for the first
		// fragments (alpha = 1/count) so a single startup/glitch fragment
		// cannot poison the estimate for seconds, then settle to the fixed
		// slow EWMA once enough history exists to be jitter-resistant.
		alpha := 1.0 / float64(d.count)
		if alpha < smoothAlpha {
			alpha = smoothAlpha
		}
		d.ewma += alpha * (mean - d.ewma)
	}
	if d.ewma < minFrameTicks {
		d.ewma = minFrameTicks
	}
	if d.ewma > maxFrameTicks {
		d.ewma = maxFrameTicks
	}

	target := int64(d.ewma + 0.5)
	ideal := target * int64(n)

	d.realClock += int64(totalTicks)
	// How far the smoothed clock trails (or leads) real time. Correct by at
	// most an eighth-frame per fragment so the cadence never visibly jumps;
	// the remainder is carried and bled off over subsequent fragments.
	drift := d.realClock - (d.smoothClock + ideal)
	maxCorr := target / 8
	if maxCorr < 1 {
		maxCorr = 1
	}
	if drift > maxCorr {
		drift = maxCorr
	} else if drift < -maxCorr {
		drift = -maxCorr
	}

	fragTotal := ideal + drift
	if fragTotal < int64(n) {
		fragTotal = int64(n) // never produce a zero/negative duration
	}

	per := fragTotal / int64(n)
	last := n - 1
	var acc int64
	for i := 0; i < last; i++ {
		samples[i].Duration = uint32(per)
		acc += per
	}
	samples[last].Duration = uint32(fragTotal - acc)

	d.smoothClock += fragTotal
	return uint32(fragTotal)
}
