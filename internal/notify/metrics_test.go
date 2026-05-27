package notify

import (
	"strings"
	"testing"
)

func TestMetricsWritePromAnnotated(t *testing.T) {
	var b strings.Builder
	NewMetrics().WriteProm(&b)
	body := b.String()
	for _, want := range []string{
		"# TYPE vedetta_notify_events_received_total counter",
		"# TYPE vedetta_notify_events_sent_total counter",
		"# TYPE vedetta_notify_push_send_total counter",
		"# TYPE vedetta_notify_subscriptions_gauge gauge",
		"# TYPE vedetta_notify_queue_depth_gauge gauge",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing annotation %q in:\n%s", want, body)
		}
	}
}
