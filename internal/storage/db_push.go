package storage

import (
	"database/sql"
	"errors"
	"time"
)

// PushSubscription is a row in push_subscriptions.
type PushSubscription struct {
	ID        int64
	Username  string
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
	CreatedAt time.Time
	LastSeen  time.Time
}

var (
	ErrPushSubscriptionNotFound = errors.New("push subscription not found")
	ErrSubscriptionOwnedByOther = errors.New("push subscription endpoint already registered to another user")
)

// SavePushSubscription inserts or updates a subscription.
// If an existing row has the same endpoint:
//   - owned by the same user: keys/user_agent are updated, last_seen bumped, same id returned.
//   - owned by a different user: ErrSubscriptionOwnedByOther is returned.
func (d *DB) SavePushSubscription(sub PushSubscription) (int64, error) {
	// Single atomic upsert: a separate SELECT-then-INSERT lets two concurrent
	// registrations of the same new endpoint both observe "no row" and then race
	// on the UNIQUE(endpoint) constraint. ON CONFLICT applies the update only
	// when the existing row belongs to the same user; a conflict owned by a
	// different user filters the update out, so RETURNING yields no row, which we
	// map to ErrSubscriptionOwnedByOther. The id is stable across updates, so
	// every caller gets the same value.
	var id int64
	err := d.db.QueryRow(`
		INSERT INTO push_subscriptions (username, endpoint, p256dh, auth, user_agent)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			user_agent = excluded.user_agent,
			last_seen = CURRENT_TIMESTAMP
		WHERE push_subscriptions.username = excluded.username
		RETURNING id`,
		sub.Username, sub.Endpoint, sub.P256dh, sub.Auth, nullString(sub.UserAgent)).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, ErrSubscriptionOwnedByOther
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// FindPushSubscriptionByEndpoint returns the subscription with the given endpoint, or nil if none.
func (d *DB) FindPushSubscriptionByEndpoint(endpoint string) (*PushSubscription, error) {
	var s PushSubscription
	var userAgent sql.NullString
	err := d.db.QueryRow(`
		SELECT id, username, endpoint, p256dh, auth, user_agent, created_at, last_seen
		  FROM push_subscriptions WHERE endpoint = ?`, endpoint).
		Scan(&s.ID, &s.Username, &s.Endpoint, &s.P256dh, &s.Auth, &userAgent, &s.CreatedAt, &s.LastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if userAgent.Valid {
		s.UserAgent = userAgent.String
	}
	return &s, nil
}

// ListPushSubscriptionsByUser returns all subscriptions for the given user.
func (d *DB) ListPushSubscriptionsByUser(username string) ([]PushSubscription, error) {
	rows, err := d.db.Query(`
		SELECT id, username, endpoint, p256dh, auth, user_agent, created_at, last_seen
		  FROM push_subscriptions WHERE username = ? ORDER BY id`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var s PushSubscription
		var userAgent sql.NullString
		if err := rows.Scan(&s.ID, &s.Username, &s.Endpoint, &s.P256dh, &s.Auth, &userAgent, &s.CreatedAt, &s.LastSeen); err != nil {
			return nil, err
		}
		if userAgent.Valid {
			s.UserAgent = userAgent.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeletePushSubscription removes a subscription by id, but only if it belongs to username.
// Returns ErrPushSubscriptionNotFound if no row matches.
func (d *DB) DeletePushSubscription(id int64, username string) error {
	res, err := d.db.Exec(`DELETE FROM push_subscriptions WHERE id = ? AND username = ?`, id, username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPushSubscriptionNotFound
	}
	return nil
}

// DeletePushSubscriptionByEndpoint removes a subscription by endpoint, regardless of owner.
// Used by the dispatcher to prune after 404/410 responses from the push service.
func (d *DB) DeletePushSubscriptionByEndpoint(endpoint string) error {
	_, err := d.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// CountPushSubscriptions returns the total number of push subscriptions across all users.
func (d *DB) CountPushSubscriptions() (int, error) {
	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions`).Scan(&n)
	return n, err
}
