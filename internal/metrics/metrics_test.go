package metrics

import (
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A histogram must place each observation in the lowest bucket whose upper
// bound is >= the observed value, emit cumulative bucket counts, a +Inf bucket
// equal to the total count, and a sum in seconds.
func TestHistogramCumulativeBucketsSumAndCount(t *testing.T) {
	h := NewHistogram("vedetta_test_seconds", "test help", []float64{0.001, 0.01})

	h.Observe("front", 500*time.Microsecond) // 0.0005s -> le=0.001
	h.Observe("front", 5*time.Millisecond)   // 0.005s  -> le=0.01
	h.Observe("front", 50*time.Millisecond)  // 0.05s   -> +Inf only

	got := render(h)
	want := strings.Join([]string{
		"# HELP vedetta_test_seconds test help",
		"# TYPE vedetta_test_seconds histogram",
		`vedetta_test_seconds_bucket{camera="front",le="0.001"} 1`,
		`vedetta_test_seconds_bucket{camera="front",le="0.01"} 2`,
		`vedetta_test_seconds_bucket{camera="front",le="+Inf"} 3`,
		`vedetta_test_seconds_sum{camera="front"} 0.0555`,
		`vedetta_test_seconds_count{camera="front"} 3`,
		"",
	}, "\n")

	if got != want {
		t.Errorf("histogram output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// An observation exactly equal to a bucket bound belongs in that bucket
// (le is "less than or equal"), and must not leak into a lower one.
func TestHistogramBoundaryIsInclusive(t *testing.T) {
	h := NewHistogram("vedetta_test_seconds", "h", []float64{0.005})

	h.Observe("c", 5*time.Millisecond) // exactly 0.005s

	got := render(h)
	if !strings.Contains(got, `vedetta_test_seconds_bucket{camera="c",le="0.005"} 1`) {
		t.Errorf("boundary observation not counted in its own bucket:\n%s", got)
	}
}

// Camera names are emitted as label values and must be escaped the same way the
// rest of /metrics escapes them (backslash, quote, newline).
func TestHistogramEscapesCameraLabel(t *testing.T) {
	h := NewHistogram("vedetta_test_seconds", "h", []float64{0.01})
	h.Observe(`cam"a\b`+"\n", time.Millisecond)

	got := render(h)
	if !strings.Contains(got, `camera="cam\"a\\b\n"`) {
		t.Errorf("camera label not escaped:\n%s", got)
	}
}

// Multiple cameras must render in a deterministic (sorted) order so the
// exposition is stable across scrapes.
func TestHistogramSortsCameras(t *testing.T) {
	h := NewHistogram("vedetta_test_seconds", "h", []float64{0.01})
	h.Observe("zebra", time.Millisecond)
	h.Observe("alpha", time.Millisecond)
	h.Observe("mike", time.Millisecond)

	got := render(h)
	ai := strings.Index(got, `camera="alpha"`)
	mi := strings.Index(got, `camera="mike"`)
	zi := strings.Index(got, `camera="zebra"`)
	if ai < 0 || ai >= mi || mi >= zi {
		t.Errorf("cameras not sorted: alpha=%d mike=%d zebra=%d\n%s", ai, mi, zi, got)
	}
}

// A counter accumulates per camera, is monotonic, renders sorted, escapes the
// label, and declares itself a counter type.
func TestCounterPerCameraMonotonicSortedEscaped(t *testing.T) {
	c := NewCounter("vedetta_test_total", "test help")
	c.Inc("beta")
	c.Add("beta", 4)
	c.Inc("alpha")

	got := render(c)
	want := strings.Join([]string{
		"# HELP vedetta_test_total test help",
		"# TYPE vedetta_test_total counter",
		`vedetta_test_total{camera="alpha"} 1`,
		`vedetta_test_total{camera="beta"} 5`,
		"",
	}, "\n")
	if got != want {
		t.Errorf("counter output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The package WriteProm renders every registered instrument; ResetForTest
// clears the accumulated series so tests do not bleed into one another.
func TestPackageWritePromAndReset(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)

	MotionDetectDuration.Observe("cam", time.Millisecond)
	YOLOInferenceDuration.Observe("cam", 20*time.Millisecond)
	FrameDecodeDuration.Observe("cam", 3*time.Millisecond)
	FramesProcessed.Inc("cam")
	FramesDecoded.Inc("cam")
	DetectInputDropped.Inc("cam")

	var b strings.Builder
	WriteProm(&b)
	out := b.String()

	for _, name := range []string{
		"vedetta_motion_detect_duration_seconds",
		"vedetta_yolo_inference_duration_seconds",
		"vedetta_frame_decode_duration_seconds",
		"vedetta_frames_processed_total",
		"vedetta_frames_decoded_total",
		"vedetta_detect_input_dropped_total",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("WriteProm output missing instrument %q", name)
		}
	}

	ResetForTest()
	b.Reset()
	WriteProm(&b)
	if strings.Contains(b.String(), `camera="cam"`) {
		t.Errorf("ResetForTest did not clear series:\n%s", b.String())
	}
}

// A scrape that races concurrent observations must always emit a mutually
// consistent snapshot: the cumulative +Inf bucket equals _count. Independent
// atomics would let a scrape land between the bucket and count updates and
// violate that invariant.
func TestHistogramSnapshotConsistentUnderConcurrency(t *testing.T) {
	h := NewHistogram("vedetta_test_seconds", "h", []float64{0.001, 0.01, 0.1})

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20000; i++ {
			h.Observe("cam", time.Duration(i%300)*time.Microsecond)
		}
		close(done)
	}()

	check := func() {
		var b strings.Builder
		h.WriteProm(&b)
		inf, count, ok := parseInfAndCount(b.String())
		if ok && inf != count {
			t.Fatalf("histogram invariant violated: +Inf bucket=%d, count=%d", inf, count)
		}
	}
	for {
		select {
		case <-done:
			check()
			return
		default:
			check()
		}
	}
}

// parseInfAndCount extracts the +Inf cumulative bucket and the _count value
// from a single-camera histogram rendering.
func parseInfAndCount(out string) (inf, count uint64, ok bool) {
	var haveInf, haveCount bool
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch {
		case strings.Contains(fields[0], `le="+Inf"`):
			inf, haveInf = val, true
		case strings.HasPrefix(fields[0], "vedetta_test_seconds_count{"):
			count, haveCount = val, true
		}
	}
	return inf, count, haveInf && haveCount
}

func render(w interface{ WriteProm(io.Writer) }) string {
	var b strings.Builder
	w.WriteProm(&b)
	return b.String()
}
