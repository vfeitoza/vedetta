package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// Sender is the narrow interface over the webpush library. Tests inject a
// fake so dispatcher_test.go stays hermetic; production wires WebPushSender.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte, vapid *VAPID) SendResult
}

// Subscription is the subset of storage.PushSubscription the sender needs.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// SendResult categorizes the outcome of a single Send call. Status is the
// HTTP status code from the push service, 0 on transport/timeout error.
type SendResult struct {
	Status  int
	Err     error
	Timeout bool
}

// NotificationDispatcher consumes confirmed-track events and fans out web
// push notifications. Callers call Start once and Enqueue per event. The
// Enqueue path is non-blocking — the detection hot path must never stall.
type NotificationDispatcher struct {
	store    Store
	sender   Sender
	vapid    *VAPID
	cooldown *CooldownCache
	metrics  *Metrics
	logger   *slog.Logger
	window   time.Duration
	jobs     chan camera.Event
	workers  int
	wg       sync.WaitGroup
}

// Options bundles dispatcher construction params. Zero values fall back to
// documented defaults (see New).
type Options struct {
	Store          Store
	Sender         Sender
	VAPID          *VAPID
	Logger         *slog.Logger
	CooldownWindow time.Duration // default 3 min
	QueueCapacity  int           // default 256
	Workers        int           // default 4
	Metrics        *Metrics      // nil → NewMetrics()
}

// New builds a dispatcher. Call Start(ctx) to launch workers.
func New(opts Options) *NotificationDispatcher {
	if opts.CooldownWindow == 0 {
		opts.CooldownWindow = 3 * time.Minute
	}
	if opts.QueueCapacity == 0 {
		opts.QueueCapacity = 256
	}
	if opts.Workers == 0 {
		opts.Workers = 4
	}
	if opts.Metrics == nil {
		opts.Metrics = NewMetrics()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &NotificationDispatcher{
		store:    opts.Store,
		sender:   opts.Sender,
		vapid:    opts.VAPID,
		cooldown: NewCooldownCache(opts.CooldownWindow, nil),
		metrics:  opts.Metrics,
		logger:   opts.Logger,
		window:   opts.CooldownWindow,
		jobs:     make(chan camera.Event, opts.QueueCapacity),
		workers:  opts.Workers,
	}
}

// Metrics returns the shared metrics struct. Used by the /metrics handler
// to render counters in Prometheus text format without reaching into the
// dispatcher's internals.
func (d *NotificationDispatcher) Metrics() *Metrics { return d.metrics }

// VAPIDPublicKey returns the base64url-encoded VAPID public key. The HTTP
// API exposes this to the browser so the service worker can subscribe.
func (d *NotificationDispatcher) VAPIDPublicKey() string {
	if d.vapid == nil {
		return ""
	}
	return d.vapid.PublicKey()
}

// Enqueue is non-blocking. If the queue is full the event is dropped and
// EventsDropped is incremented. The detection hot path must never be stalled
// on notification fan-out.
func (d *NotificationDispatcher) Enqueue(ev camera.Event) {
	d.metrics.EventsReceived.Add(1)
	select {
	case d.jobs <- ev:
		d.metrics.QueueDepth.Store(int64(len(d.jobs)))
	default:
		d.metrics.EventsDropped.Add(1)
		d.logger.Warn("notification queue full, dropping event",
			"event", ev.ID, "camera", ev.CameraName, "label", ev.Label)
	}
}

// EnqueueTest synthesizes a test event and runs it through the normal
// dispatch path. It fans out to ALL users with subscriptions — this is
// acceptable because the test button is an admin action triggered
// explicitly by the operator.
func (d *NotificationDispatcher) EnqueueTest(username, cameraName string) {
	if cameraName == "" {
		cameraName = "test"
	}
	ev := camera.Event{
		ID:                fmt.Sprintf("test-%d", time.Now().UnixNano()),
		CameraName:        cameraName,
		Label:             "person",
		Timestamp:         time.Now().UTC(),
		SnapshotAvailable: false,
	}
	d.Enqueue(ev)
}

// Start launches worker goroutines and a periodic cooldown sweeper.
// Returns immediately; workers run until ctx is cancelled.
func (d *NotificationDispatcher) Start(ctx context.Context) {
	for i := 0; i < d.workers; i++ {
		d.wg.Add(1)
		go d.workerLoop(ctx, i)
	}
	d.wg.Add(1)
	go d.sweepLoop(ctx)
}

// Wait blocks until all workers and the sweeper have exited. Callers should
// cancel the ctx passed to Start first.
func (d *NotificationDispatcher) Wait() { d.wg.Wait() }

func (d *NotificationDispatcher) sweepLoop(ctx context.Context) {
	defer d.wg.Done()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.cooldown.Sweep()
			if n, err := d.store.CountPushSubscriptions(); err == nil {
				d.metrics.SubscriptionCount.Store(int64(n))
			}
		}
	}
}

func (d *NotificationDispatcher) workerLoop(ctx context.Context, id int) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-d.jobs:
			d.metrics.QueueDepth.Store(int64(len(d.jobs)))
			d.handleEvent(ctx, ev)
		}
	}
}

// handleEvent implements the per-event dispatch logic described in the
// design spec (each worker's inner loop, steps 1–7). A panic in any one
// event's handling must not kill the worker.
func (d *NotificationDispatcher) handleEvent(ctx context.Context, ev camera.Event) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("panic in notification worker",
				"event", ev.ID, "panic", r)
			d.metrics.PushSendError.Add(1)
		}
	}()

	users, err := d.store.ListAllUsernames()
	if err != nil {
		d.logger.Error("list users", "error", err)
		return
	}
	payload := BuildPayload(ev)

	for _, user := range users {
		d.dispatchToUser(ctx, user, ev, payload)
	}
}

func (d *NotificationDispatcher) dispatchToUser(ctx context.Context, user string, ev camera.Event, payload []byte) {
	// 1. Mute check.
	if muted, _, _ := d.store.GetKV("notify:" + user + ":muted"); muted == "1" {
		d.metrics.EventsMuted.Add(1)
		return
	}
	// 2. Pref check (wildcard-aware via storage layer).
	enabled, err := d.store.IsNotificationEnabled(user, ev.CameraName, ev.Label)
	if err != nil {
		d.logger.Error("pref check", "user", user, "error", err)
		return
	}
	if !enabled {
		d.metrics.EventsDisabled.Add(1)
		return
	}
	// 3. Cooldown check.
	key := user + ":" + ev.CameraName + ":" + ev.Label
	if d.cooldown.Check(key) {
		d.metrics.EventsCooldown.Add(1)
		return
	}
	// 4. Load subscriptions.
	subs, err := d.store.ListPushSubscriptionsByUser(user)
	if err != nil {
		d.logger.Error("list subs", "user", user, "error", err)
		return
	}
	if len(subs) == 0 {
		d.metrics.EventsDisabled.Add(1)
		return
	}

	// 5/6. Send to each subscription. Mark cooldown only if >=1 success —
	// a total failure (all 5xx or timeout) should not suppress the retry
	// opportunity on the next event.
	anySuccess := false
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, sub := range subs {
		// Per-endpoint backoff map (shared with the cooldown cache). 429s
		// set this key so subsequent events skip the offending endpoint
		// until its backoff window expires.
		backoffKey := "backoff:" + sub.Endpoint
		if d.cooldown.Check(backoffKey) {
			continue
		}
		result := d.sender.Send(sendCtx, Subscription{
			Endpoint: sub.Endpoint,
			P256dh:   sub.P256dh,
			Auth:     sub.Auth,
		}, payload, d.vapid)
		d.recordResult(result, sub.Endpoint)
		switch {
		case result.Status >= 200 && result.Status < 300:
			anySuccess = true
		case result.Status == 404 || result.Status == 410:
			if err := d.store.DeletePushSubscriptionByEndpoint(sub.Endpoint); err != nil {
				d.logger.Error("prune sub", "error", err)
			}
		case result.Status == 429:
			d.cooldown.Mark(backoffKey)
		}
	}
	if anySuccess {
		d.cooldown.Mark(key)
		d.metrics.EventsSent.Add(1)
	}
}

func (d *NotificationDispatcher) recordResult(r SendResult, endpoint string) {
	switch {
	case r.Timeout:
		d.metrics.PushSendTimeout.Add(1)
	case r.Err != nil:
		d.metrics.PushSendError.Add(1)
		d.logger.Warn("push send error",
			"endpoint_host", hostOnly(endpoint), "error", r.Err)
	case r.Status == 401 || r.Status == 403:
		d.metrics.PushSend401.Add(1)
		d.logger.Error("push send 401/403",
			"endpoint_host", hostOnly(endpoint), "status", r.Status)
	case r.Status == 410 || r.Status == 404:
		d.metrics.PushSend410.Add(1)
	case r.Status == 429:
		d.metrics.PushSend429.Add(1)
	case r.Status >= 200 && r.Status < 300:
		d.metrics.PushSendOK.Add(1)
	default:
		d.metrics.PushSendError.Add(1)
		d.logger.Warn("push send unexpected status",
			"endpoint_host", hostOnly(endpoint), "status", r.Status)
	}
}

// hostOnly extracts the host portion of an endpoint URL without pulling in
// net/url. Logging full endpoints would leak unique subscription IDs; we
// keep just enough to identify which push service was involved.
func hostOnly(endpoint string) string {
	for i := 0; i < len(endpoint); i++ {
		if endpoint[i] == '/' && i+1 < len(endpoint) && endpoint[i+1] == '/' {
			rest := endpoint[i+2:]
			for j := 0; j < len(rest); j++ {
				if rest[j] == '/' {
					return rest[:j]
				}
			}
			return rest
		}
	}
	return endpoint
}
