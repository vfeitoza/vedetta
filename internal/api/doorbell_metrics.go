package api

import "sync"

// doorbellMetrics tracks per-camera doorbell press counts (monotonic counter) and
// currently-unanswered rings (gauge). Mirrors mjpegViewers: zero-valued gauge
// series are deleted so /metrics omits stale zero lines.
type doorbellMetrics struct {
	mu         sync.Mutex
	presses    map[string]int64
	unanswered map[string]int
}

func newDoorbellMetrics() *doorbellMetrics {
	return &doorbellMetrics{presses: map[string]int64{}, unanswered: map[string]int{}}
}

func (d *doorbellMetrics) recordPress(camera string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.presses[camera]++
	d.unanswered[camera]++
}

func (d *doorbellMetrics) recordAnswer(camera string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.unanswered[camera] > 0 {
		d.unanswered[camera]--
	}
	if d.unanswered[camera] <= 0 {
		delete(d.unanswered, camera)
	}
}

func (d *doorbellMetrics) pressCounts() map[string]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]int64, len(d.presses))
	for k, v := range d.presses {
		out[k] = v
	}
	return out
}

func (d *doorbellMetrics) unansweredCounts() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]int, len(d.unanswered))
	for k, v := range d.unanswered {
		out[k] = v
	}
	return out
}

func (d *doorbellMetrics) unansweredCount(camera string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.unanswered[camera]
}
