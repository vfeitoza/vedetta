package api

import "sync"

// mjpegViewers tracks active MJPEG streaming clients per camera for /metrics.
// MJPEG is a long-lived HTTP response with no manager object, so the count is
// maintained at the handler boundary. A camera drops out of the map when its
// count returns to zero so /metrics emits no stale zero series.
type mjpegViewers struct {
	mu sync.Mutex
	n  map[string]int
}

func newMJPEGViewers() *mjpegViewers { return &mjpegViewers{n: make(map[string]int)} }

func (v *mjpegViewers) add(camera string) {
	v.mu.Lock()
	v.n[camera]++
	v.mu.Unlock()
}

func (v *mjpegViewers) remove(camera string) {
	v.mu.Lock()
	if v.n[camera]--; v.n[camera] <= 0 {
		delete(v.n, camera)
	}
	v.mu.Unlock()
}

func (v *mjpegViewers) counts() map[string]int {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make(map[string]int, len(v.n))
	for k, c := range v.n {
		out[k] = c
	}
	return out
}
