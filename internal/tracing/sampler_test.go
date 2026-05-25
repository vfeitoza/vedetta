package tracing

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func decision(s sdktrace.Sampler, ctx context.Context, name string) sdktrace.SamplingDecision {
	return s.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: ctx,
		TraceID:       trace.TraceID{0x01},
		Name:          name,
	}).Decision
}

// TestSamplerAlwaysSamples pins the sampler contract: every span name is
// sampled, with no name-based or ratio throttling. Detection volume is handled
// with metrics, not spans, so no span name is special-cased. A "detect."-named
// span is included to guard against reintroducing prefix routing.
func TestSamplerAlwaysSamples(t *testing.T) {
	s := newSampler()
	for _, name := range []string{"event", "clip.extract", "vedetta-api", "detect.inference"} {
		if got := decision(s, context.Background(), name); got != sdktrace.RecordAndSample {
			t.Errorf("newSampler sample %q => %v, want RecordAndSample", name, got)
		}
	}
}
