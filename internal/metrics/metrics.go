// Package metrics holds the detection-pipeline latency histograms and counters
// exposed through Vedetta's hand-rolled Prometheus-text /metrics endpoint.
//
// The NVR hot path runs per frame, per camera, so spans would flood the trace
// backend; latency histograms and counters are the right tool. Instruments are
// goroutine-safe and cheap to record from the decode and detection goroutines.
// They are deliberately self-contained (no prometheus/client_golang dependency)
// to match the project's minimal-dependency posture and the existing
// hand-rolled exposition in internal/notify.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// promInstrument is anything that can render itself as Prometheus text and be
// cleared between tests.
type promInstrument interface {
	WriteProm(io.Writer)
	reset()
}

// registry holds the package-level instruments in declaration order so the
// exposition is stable across scrapes.
var registry []promInstrument

func register[T promInstrument](inst T) T {
	registry = append(registry, inst)
	return inst
}

// Package-level instruments. Recorded directly from the hot path; exposed via
// WriteProm from internal/api GetMetrics.
var (
	// MotionDetectDuration times contour-based motion detection per frame.
	MotionDetectDuration = register(NewHistogram(
		"vedetta_motion_detect_duration_seconds",
		"Time spent in contour-based motion detection per frame.",
		[]float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1},
	))
	// YOLOInferenceDuration times YOLO inference, which only runs on frames
	// with qualified motion.
	YOLOInferenceDuration = register(NewHistogram(
		"vedetta_yolo_inference_duration_seconds",
		"Time spent in YOLO object-detection inference per frame.",
		[]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
	))
	// FrameDecodeDuration times H.264 decode plus YUV-to-RGB scaling in the
	// detection consumer.
	FrameDecodeDuration = register(NewHistogram(
		"vedetta_frame_decode_duration_seconds",
		"Time spent decoding and scaling a frame for detection.",
		[]float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1},
	))

	// FramesProcessed counts frames that reach the detection pipeline.
	FramesProcessed = register(NewCounter(
		"vedetta_frames_processed_total",
		"Frames processed by the detection pipeline.",
	))
	// FramesDecoded counts frames successfully decoded for detection.
	FramesDecoded = register(NewCounter(
		"vedetta_frames_decoded_total",
		"Frames successfully decoded for detection.",
	))
	// DetectInputDropped counts decoded frames dropped because the detection
	// pipeline was busy (frame channel full).
	DetectInputDropped = register(NewCounter(
		"vedetta_detect_input_dropped_total",
		"Decoded frames dropped because the detection pipeline was busy.",
	))
)

// WriteProm renders every registered instrument in Prometheus text format,
// appending to w. Called from internal/api/handler_health.go GetMetrics.
func WriteProm(w io.Writer) {
	for _, inst := range registry {
		inst.WriteProm(w)
	}
}

// ResetForTest clears all registered instruments. Test-only: prevents series
// from one test bleeding into another that scrapes the package endpoint.
func ResetForTest() {
	for _, inst := range registry {
		inst.reset()
	}
}

// Histogram is a per-camera latency histogram with fixed, ascending second
// bounds. It observes a time.Duration and is safe for concurrent use.
type Histogram struct {
	name     string
	help     string
	bounds   []float64 // upper bounds in seconds, ascending, excludes +Inf
	boundsNs []int64   // same bounds in nanoseconds, for exact comparison
	series   sync.Map  // camera string -> *histSeries
}

type histSeries struct {
	// mu guards all three fields together so a scrape always observes a
	// consistent snapshot. Without it the bucket, sum, and count could be read
	// mid-update, emitting a +Inf bucket greater than _count or a stale _sum -
	// both Prometheus histogram-invariant violations. Contention is negligible:
	// effectively one writer goroutine per camera per instrument.
	mu sync.Mutex
	// counts has len(bounds)+1 entries: one per bound plus a trailing +Inf
	// overflow bucket. Stored non-cumulatively; WriteProm accumulates.
	counts []uint64
	sumNs  int64
	count  uint64
}

// NewHistogram builds a standalone histogram. bounds must be ascending second
// values and exclude +Inf (it is appended implicitly).
func NewHistogram(name, help string, bounds []float64) *Histogram {
	boundsNs := make([]int64, len(bounds))
	for i, b := range bounds {
		boundsNs[i] = int64(b * float64(time.Second))
	}
	return &Histogram{name: name, help: help, bounds: bounds, boundsNs: boundsNs}
}

// Observe records one duration for the given camera.
func (h *Histogram) Observe(camera string, d time.Duration) {
	s := h.seriesFor(camera)
	ns := d.Nanoseconds()

	// First bucket whose upper bound is >= the observation; else the +Inf
	// overflow bucket (last index).
	idx := len(h.boundsNs)
	for i, bn := range h.boundsNs {
		if ns <= bn {
			idx = i
			break
		}
	}
	s.mu.Lock()
	s.counts[idx]++
	s.sumNs += ns
	s.count++
	s.mu.Unlock()
}

func (h *Histogram) seriesFor(camera string) *histSeries {
	if v, ok := h.series.Load(camera); ok {
		return v.(*histSeries)
	}
	s := &histSeries{counts: make([]uint64, len(h.bounds)+1)}
	actual, _ := h.series.LoadOrStore(camera, s)
	return actual.(*histSeries)
}

// WriteProm renders the histogram in Prometheus text format, appending to w.
func (h *Histogram) WriteProm(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n", h.name, h.help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", h.name)

	for _, camera := range h.sortedCameras() {
		v, _ := h.series.Load(camera)
		s := v.(*histSeries)
		label := escapeLabel(camera)

		// Snapshot the series under its lock so the emitted buckets, sum, and
		// count are mutually consistent even while frames are being observed.
		s.mu.Lock()
		counts := make([]uint64, len(s.counts))
		copy(counts, s.counts)
		sumNs := s.sumNs
		total := s.count
		s.mu.Unlock()

		var cumulative uint64
		for i, b := range h.bounds {
			cumulative += counts[i]
			fmt.Fprintf(w, "%s_bucket{camera=\"%s\",le=\"%s\"} %d\n",
				h.name, label, formatBound(b), cumulative)
		}
		// +Inf overflow bucket equals the total count.
		cumulative += counts[len(h.bounds)]
		fmt.Fprintf(w, "%s_bucket{camera=\"%s\",le=\"+Inf\"} %d\n", h.name, label, cumulative)

		sum := float64(sumNs) / float64(time.Second)
		fmt.Fprintf(w, "%s_sum{camera=\"%s\"} %s\n", h.name, label, formatFloat(sum))
		fmt.Fprintf(w, "%s_count{camera=\"%s\"} %d\n", h.name, label, total)
	}
}

func (h *Histogram) sortedCameras() []string {
	var cameras []string
	h.series.Range(func(k, _ any) bool {
		cameras = append(cameras, k.(string))
		return true
	})
	sort.Strings(cameras)
	return cameras
}

func (h *Histogram) reset() {
	h.series.Range(func(k, _ any) bool {
		h.series.Delete(k)
		return true
	})
}

// Counter is a per-camera monotonic counter, safe for concurrent use.
type Counter struct {
	name   string
	help   string
	series sync.Map // camera string -> *atomic.Int64
}

// NewCounter builds a standalone counter.
func NewCounter(name, help string) *Counter {
	return &Counter{name: name, help: help}
}

// Inc adds one to the camera's counter.
func (c *Counter) Inc(camera string) { c.Add(camera, 1) }

// Add adds n to the camera's counter.
func (c *Counter) Add(camera string, n int64) {
	v, ok := c.series.Load(camera)
	if !ok {
		v, _ = c.series.LoadOrStore(camera, new(atomic.Int64))
	}
	v.(*atomic.Int64).Add(n)
}

// WriteProm renders the counter in Prometheus text format, appending to w.
func (c *Counter) WriteProm(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n", c.name, c.help)
	fmt.Fprintf(w, "# TYPE %s counter\n", c.name)

	var cameras []string
	c.series.Range(func(k, _ any) bool {
		cameras = append(cameras, k.(string))
		return true
	})
	sort.Strings(cameras)

	for _, camera := range cameras {
		v, _ := c.series.Load(camera)
		fmt.Fprintf(w, "%s{camera=\"%s\"} %d\n", c.name, escapeLabel(camera), v.(*atomic.Int64).Load())
	}
}

func (c *Counter) reset() {
	c.series.Range(func(k, _ any) bool {
		c.series.Delete(k)
		return true
	})
}

// escapeLabel escapes a label value the same way the rest of /metrics does:
// backslash, double-quote, and newline.
func escapeLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}

// formatBound renders a bucket upper bound for the le label.
func formatBound(b float64) string {
	return strconv.FormatFloat(b, 'g', -1, 64)
}

// formatFloat renders a sum value with shortest round-trip precision.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
