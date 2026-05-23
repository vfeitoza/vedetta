package tracing

import (
	"strings"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// prefixSampler routes the head-sampling decision by root span name: detection
// spans are ratio-sampled to bound per-event volume, everything else (HTTP,
// event, push, internal) is always sampled.
type prefixSampler struct {
	detect sdktrace.Sampler
	other  sdktrace.Sampler
}

func (s prefixSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if strings.HasPrefix(p.Name, "detect.") {
		return s.detect.ShouldSample(p)
	}
	return s.other.ShouldSample(p)
}

func (s prefixSampler) Description() string {
	return "vedetta.prefixSampler{detect=ratio,other=always}"
}

// newSampler builds the composite sampler wrapped in ParentBased so children
// inherit the root decision. Detection spans must therefore be created as roots
// to receive ratio sampling.
func newSampler(ratio float64) sdktrace.Sampler {
	return sdktrace.ParentBased(prefixSampler{
		detect: sdktrace.TraceIDRatioBased(ratio),
		other:  sdktrace.AlwaysSample(),
	})
}
