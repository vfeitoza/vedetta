package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/notify"
	"github.com/rvben/vedetta/internal/storage"
)

// newTestServerWithUser builds a test server with one seeded auth user.
// The underlying DB is cleaned up by t.Cleanup registered inside
// newTestServer.
func newTestServerWithUser(t *testing.T, username string) (*Server, *storage.DB) {
	t.Helper()
	return newTestServerWithUsers(t, username)
}

// newTestServerWithUsers builds a test server with the given auth users
// seeded into the auth_users table. The returned DB handle is suitable
// for direct seeding of push subscriptions and preference rows.
func newTestServerWithUsers(t *testing.T, usernames ...string) (*Server, *storage.DB) {
	t.Helper()
	srv, db := newTestServer(t)
	for _, u := range usernames {
		if err := db.SaveAuthUser(u, "bcrypt-hash-placeholder"); err != nil {
			t.Fatalf("seed auth user %q: %v", u, err)
		}
	}
	return srv, db
}

// withSessionPrincipal injects a session-kind principal into the request
// context, mimicking what authMiddleware does after a real login.
func withSessionPrincipal(r *http.Request, username string) *http.Request {
	p := &auth.Principal{Username: username, Kind: auth.AuthKindSession}
	ctx := context.WithValue(r.Context(), principalContextKey{}, p)
	return r.WithContext(ctx)
}

// withTokenPrincipal injects a token-kind principal — used by tests that
// verify push endpoints explicitly reject non-session principals.
func withTokenPrincipal(r *http.Request, username string) *http.Request {
	p := &auth.Principal{Username: username, Kind: auth.AuthKindToken}
	ctx := context.WithValue(r.Context(), principalContextKey{}, p)
	return r.WithContext(ctx)
}

// withProxyPrincipal injects a proxy-kind principal — the form Vedetta sees
// when a request arrives via a trusted reverse proxy (Authelia + Caddy). Push
// endpoints must accept this kind so the PWA works in that deployment.
func withProxyPrincipal(r *http.Request, username string) *http.Request {
	p := &auth.Principal{Username: username, Kind: auth.AuthKindProxy}
	ctx := context.WithValue(r.Context(), principalContextKey{}, p)
	return r.WithContext(ctx)
}

// noopSender satisfies notify.Sender without making network calls. Tests
// that only care about the handler-side path wire this so the dispatcher
// can be constructed without a live push service.
type noopSender struct{}

func (noopSender) Send(_ context.Context, _ notify.Subscription, _ []byte, _ *notify.VAPID) notify.SendResult {
	return notify.SendResult{Status: 200}
}

// newNoopDispatcherForTest builds a minimal NotificationDispatcher backed by
// the supplied storage.DB. It never starts workers, so Enqueue'd events pile
// up harmlessly in the job channel — callers that only exercise the handler
// layer don't need dispatch to actually run.
func newNoopDispatcherForTest(t *testing.T, db *storage.DB) *notify.NotificationDispatcher {
	t.Helper()
	vapid, err := notify.LoadOrGenerateVAPID(db)
	if err != nil {
		t.Fatalf("vapid: %v", err)
	}
	return notify.New(notify.Options{
		Store:  db,
		Sender: noopSender{},
		VAPID:  vapid,
	})
}
