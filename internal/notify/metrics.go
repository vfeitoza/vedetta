package notify

import (
	"fmt"
	"io"
	"sync/atomic"
)

// Metrics holds the atomic counters exposed through Vedetta's existing
// Prometheus-text /metrics endpoint. Keep this struct lock-free and
// cheap to mutate — it's hit on every dispatch attempt.
type Metrics struct {
	EventsReceived    atomic.Int64
	EventsSent        atomic.Int64 // 2xx at least once
	EventsCooldown    atomic.Int64
	EventsMuted       atomic.Int64
	EventsDisabled    atomic.Int64 // zero subs or pref disabled
	EventsDropped     atomic.Int64 // queue full
	PushSendOK        atomic.Int64
	PushSend410       atomic.Int64
	PushSend401       atomic.Int64
	PushSend429       atomic.Int64
	PushSendTimeout   atomic.Int64
	PushSendError     atomic.Int64
	SubscriptionCount atomic.Int64 // gauge — set from outside
	QueueDepth        atomic.Int64 // gauge — set from outside
}

// NewMetrics returns a zeroed Metrics.
func NewMetrics() *Metrics { return &Metrics{} }

// WriteProm renders the counters in Prometheus text format, appending to w.
// Called from internal/api/handler_health.go GetMetrics.
func (m *Metrics) WriteProm(w io.Writer) {
	fmt.Fprintf(w, "vedetta_notify_events_received_total %d\n", m.EventsReceived.Load())
	fmt.Fprintf(w, "vedetta_notify_events_sent_total{result=\"sent\"} %d\n", m.EventsSent.Load())
	fmt.Fprintf(w, "vedetta_notify_events_sent_total{result=\"cooldown\"} %d\n", m.EventsCooldown.Load())
	fmt.Fprintf(w, "vedetta_notify_events_sent_total{result=\"muted\"} %d\n", m.EventsMuted.Load())
	fmt.Fprintf(w, "vedetta_notify_events_sent_total{result=\"disabled\"} %d\n", m.EventsDisabled.Load())
	fmt.Fprintf(w, "vedetta_notify_events_sent_total{result=\"dropped\"} %d\n", m.EventsDropped.Load())
	fmt.Fprintf(w, "vedetta_notify_push_send_total{status=\"ok\"} %d\n", m.PushSendOK.Load())
	fmt.Fprintf(w, "vedetta_notify_push_send_total{status=\"410\"} %d\n", m.PushSend410.Load())
	fmt.Fprintf(w, "vedetta_notify_push_send_total{status=\"401\"} %d\n", m.PushSend401.Load())
	fmt.Fprintf(w, "vedetta_notify_push_send_total{status=\"429\"} %d\n", m.PushSend429.Load())
	fmt.Fprintf(w, "vedetta_notify_push_send_total{status=\"timeout\"} %d\n", m.PushSendTimeout.Load())
	fmt.Fprintf(w, "vedetta_notify_push_send_total{status=\"error\"} %d\n", m.PushSendError.Load())
	fmt.Fprintf(w, "vedetta_notify_subscriptions_gauge %d\n", m.SubscriptionCount.Load())
	fmt.Fprintf(w, "vedetta_notify_queue_depth_gauge %d\n", m.QueueDepth.Load())
}
