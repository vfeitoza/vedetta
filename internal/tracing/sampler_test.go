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

func TestPrefixSamplerRouting(t *testing.T) {
	s := prefixSampler{
		detect: sdktrace.TraceIDRatioBased(0), // never
		other:  sdktrace.AlwaysSample(),
	}
	if got := decision(s, context.Background(), "detect.inference"); got != sdktrace.Drop {
		t.Errorf("detect.* with ratio 0 => %v, want Drop", got)
	}
	if got := decision(s, context.Background(), "event.process"); got != sdktrace.RecordAndSample {
		t.Errorf("event.* => %v, want RecordAndSample", got)
	}
	if got := decision(s, context.Background(), "push.dispatch"); got != sdktrace.RecordAndSample {
		t.Errorf("push.* => %v, want RecordAndSample", got)
	}

	s2 := prefixSampler{detect: sdktrace.TraceIDRatioBased(1), other: sdktrace.AlwaysSample()}
	if got := decision(s2, context.Background(), "detect.inference"); got != sdktrace.RecordAndSample {
		t.Errorf("detect.* with ratio 1 => %v, want RecordAndSample", got)
	}
}

func TestParentBasedInheritance(t *testing.T) {
	// newSampler wraps the prefix sampler in ParentBased: a child of a sampled
	// parent is sampled regardless of name. This documents why detect.inference
	// MUST be a root span (a detect span under a sampled event parent would
	// bypass ratio sampling).
	root := newSampler(0) // detect ratio 0 at the root
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x02},
		SpanID:     trace.SpanID{0x02},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)
	if got := decision(root, ctx, "detect.inference"); got != sdktrace.RecordAndSample {
		t.Errorf("detect child of sampled parent => %v, want RecordAndSample (inherited)", got)
	}
	// A parentless detect span gets the ratio decision (0 => Drop).
	if got := decision(root, context.Background(), "detect.inference"); got != sdktrace.Drop {
		t.Errorf("parentless detect.* with ratio 0 => %v, want Drop", got)
	}
}
