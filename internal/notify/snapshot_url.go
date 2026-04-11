package notify

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

const (
	snapshotSigningKeyKey = "notify:snapshot_signing_key"
	// SnapshotURLTTL defines how long a signed snapshot URL stays valid.
	// Long enough that a user who opens a notification hours later still
	// sees the thumbnail; short enough that a leaked URL stops working
	// within a day. RFC 8030 caps web push TTL at 30 days; we use 24h.
	SnapshotURLTTL = 24 * time.Hour
)

// SnapshotSigner produces and verifies HMAC-signed URLs for push
// notification thumbnails. The key is persisted in kv_store and loaded
// on first access. Keys are not rotated automatically — see the rekey
// policy in the design spec.
type SnapshotSigner struct {
	key []byte
	now func() time.Time
}

// LoadOrGenerateSnapshotSigner returns the signer, generating and
// persisting a fresh 32-byte HMAC key on first startup.
func LoadOrGenerateSnapshotSigner(store KVStore) (*SnapshotSigner, error) {
	val, ok, err := store.GetKV(snapshotSigningKeyKey)
	if err != nil {
		return nil, fmt.Errorf("snapshot signer: read key: %w", err)
	}
	var key []byte
	if ok {
		key, err = base64.RawURLEncoding.DecodeString(val)
		if err != nil {
			return nil, fmt.Errorf("snapshot signer: decode stored key: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("snapshot signer: stored key has wrong length %d", len(key))
		}
	} else {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("snapshot signer: generate: %w", err)
		}
		if err := store.SetKV(snapshotSigningKeyKey, base64.RawURLEncoding.EncodeToString(key)); err != nil {
			return nil, fmt.Errorf("snapshot signer: persist key: %w", err)
		}
	}
	return &SnapshotSigner{key: key, now: time.Now}, nil
}

// Sign returns a URL path (starts with /) that iOS can fetch anonymously
// to render the notification thumbnail. Format:
//
//	/api/push/snapshot/<eventID>?e=<unixExpiry>&s=<hmacBase64>
func (s *SnapshotSigner) Sign(eventID string) string {
	exp := s.now().Add(SnapshotURLTTL).Unix()
	sig := s.computeMAC(eventID, exp)
	return fmt.Sprintf("/api/push/snapshot/%s?e=%d&s=%s",
		url.PathEscape(eventID), exp, sig)
}

// Verify returns nil if the signature over (eventID, exp) is valid and
// the expiry is in the future. Returns an error otherwise. Constant-time
// comparison to prevent timing oracles.
func (s *SnapshotSigner) Verify(eventID, expStr, sig string) error {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("invalid expiry")
	}
	if s.now().Unix() > exp {
		return errors.New("expired")
	}
	expected := s.computeMAC(eventID, exp)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return errors.New("bad signature")
	}
	return nil
}

func (s *SnapshotSigner) computeMAC(eventID string, exp int64) string {
	h := hmac.New(sha256.New, s.key)
	_, _ = fmt.Fprintf(h, "%s|%d", eventID, exp)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
