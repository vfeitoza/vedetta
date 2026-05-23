// Package backoff provides small helpers for reconnect timing shared across the
// subsystems that maintain long-lived connections (RTSP sources, ONVIF event
// subscribers).
package backoff

import "time"

// Jitter scales d into the half-open range [d/2, d) using frac, which must be in
// [0,1) (e.g. math/rand/v2.Float64()). Spreading each client's wait across this
// window desynchronizes reconnect storms when many clients share one backoff
// schedule and fail together (NVR restart, switch reboot, network blip). A
// non-positive d is returned unchanged.
func Jitter(d time.Duration, frac float64) time.Duration {
	if d <= 0 {
		return d
	}
	half := d / 2
	return half + time.Duration(frac*float64(half))
}
