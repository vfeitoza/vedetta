package notify

import "github.com/rvben/vedetta/internal/storage"

// Store is the narrow subset of *storage.DB the dispatcher needs. Tests
// implement it directly with fakes instead of spinning up a real SQLite DB
// for every case. Production wires *storage.DB, which satisfies this
// interface without any adapter.
//
// KVStore (defined in vapid.go) is embedded so the dispatcher can reuse the
// same persistence surface for per-user mute flags.
type Store interface {
	KVStore
	ListAllUsernames() ([]string, error)
	IsNotificationEnabled(username, camera, class string) (bool, error)
	ListPushSubscriptionsByUser(username string) ([]storage.PushSubscription, error)
	DeletePushSubscriptionByEndpoint(endpoint string) error
	CountPushSubscriptions() (int, error)
}
