// Package notify owns the Vedetta web push notification pipeline.
// See docs/superpowers/specs/2026-04-11-installable-pwa-with-push-notifications-design.md
// for the full design rationale.
package notify

import (
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"fmt"

	webpush "github.com/SherClockHolmes/webpush-go"
)

const (
	vapidPublicKeyKey  = "notify:vapid_public_key"
	vapidPrivateKeyKey = "notify:vapid_private_key"
)

// KVStore is the narrow subset of the storage layer that the notify package
// needs for persisting VAPID keys and other small bits of durable state.
// Tests implement it with a map; production wires *storage.DB.
type KVStore interface {
	GetKV(key string) (value string, ok bool, err error)
	SetKV(key, value string) error
}

// VAPID holds the application server's VAPID keypair in the form
// webpush-go expects (base64url strings without padding).
type VAPID struct {
	publicKey  string
	privateKey string
}

// PublicKey returns the base64url-encoded VAPID public key that clients
// must pass to pushManager.subscribe.
func (v *VAPID) PublicKey() string { return v.publicKey }

// PrivateKey returns the base64url-encoded VAPID private key used by
// webpush-go to sign outgoing push requests.
func (v *VAPID) PrivateKey() string { return v.privateKey }

// LoadOrGenerateVAPID returns the persisted keypair, generating a fresh one on
// first start. A corrupt or unreadable stored key is a hard error — see the
// "Rekey policy" section of the design spec. Callers (notify.Start) should
// propagate the error and refuse to start the dispatcher; the rest of Vedetta
// continues running with push disabled.
func LoadOrGenerateVAPID(store KVStore) (*VAPID, error) {
	pub, pubOK, err := store.GetKV(vapidPublicKeyKey)
	if err != nil {
		return nil, fmt.Errorf("vapid: read public key: %w", err)
	}
	priv, privOK, err := store.GetKV(vapidPrivateKeyKey)
	if err != nil {
		return nil, fmt.Errorf("vapid: read private key: %w", err)
	}
	if !pubOK && !privOK {
		return generateAndStore(store)
	}
	if !pubOK || !privOK {
		return nil, errors.New("vapid: one of the keypair halves is missing; operator intervention required")
	}
	if err := validateVAPID(pub, priv); err != nil {
		return nil, fmt.Errorf("vapid: stored keys are corrupt: %w — operator intervention required", err)
	}
	return &VAPID{publicKey: pub, privateKey: priv}, nil
}

// RekeyVAPID generates a new keypair, clears all existing subscriptions via the
// supplied callback (same transaction isn't possible across the store boundary,
// so the callback runs first and the keys are persisted only on success), and
// returns the new VAPID. Intended to be called from an admin entrypoint, never
// automatically.
func RekeyVAPID(store KVStore, clearSubscriptions func() error) (*VAPID, error) {
	if err := clearSubscriptions(); err != nil {
		return nil, fmt.Errorf("vapid: clear subscriptions: %w", err)
	}
	return generateAndStore(store)
}

func generateAndStore(store KVStore) (*VAPID, error) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("vapid: generate: %w", err)
	}
	if err := store.SetKV(vapidPublicKeyKey, pub); err != nil {
		return nil, fmt.Errorf("vapid: persist public key: %w", err)
	}
	if err := store.SetKV(vapidPrivateKeyKey, priv); err != nil {
		return nil, fmt.Errorf("vapid: persist private key: %w", err)
	}
	return &VAPID{publicKey: pub, privateKey: priv}, nil
}

// validateVAPID checks that both halves decode cleanly and that the public key
// is a valid 65-byte uncompressed P-256 point.
func validateVAPID(pub, priv string) error {
	pubBytes, err := base64.RawURLEncoding.DecodeString(pub)
	if err != nil {
		return fmt.Errorf("public key not base64url: %w", err)
	}
	if len(pubBytes) != 65 || pubBytes[0] != 0x04 {
		return errors.New("public key is not a valid uncompressed P-256 point")
	}
	privBytes, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		return fmt.Errorf("private key not base64url: %w", err)
	}
	if len(privBytes) != 32 {
		return errors.New("private key is not 32 bytes")
	}
	// Final sanity: P-256 accepts the scalar.
	if _, err := ecdh.P256().NewPrivateKey(privBytes); err != nil {
		return fmt.Errorf("private key rejected by P-256: %w", err)
	}
	return nil
}
