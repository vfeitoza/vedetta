package tracing

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newSampler returns the head sampler. Vedetta samples every trace: the event
// lifecycle, clip extraction, and HTTP traces are all low-volume (HTTP is
// pre-filtered in the API middleware), so there is no name-based or ratio
// throttling. Per-frame detection volume is measured with metrics, not spans,
// so no span name needs to be sampled down.
func newSampler() sdktrace.Sampler {
	return sdktrace.AlwaysSample()
}
