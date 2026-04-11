package notify

import (
	"strings"
	"testing"
)

func TestVAPID_GenerateOnFirstStart(t *testing.T) {
	store := newFakeKVStore()
	v, err := LoadOrGenerateVAPID(store)
	if err != nil {
		t.Fatalf("load/generate: %v", err)
	}
	if v.PublicKey() == "" {
		t.Fatalf("public key empty")
	}
	// Second call should reload, not regenerate.
	v2, err := LoadOrGenerateVAPID(store)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if v2.PublicKey() != v.PublicKey() {
		t.Fatalf("reload produced different key")
	}
}

func TestVAPID_CorruptKeyFailsClosed(t *testing.T) {
	store := newFakeKVStore()
	store.set(vapidPublicKeyKey, "not-base64url")
	store.set(vapidPrivateKeyKey, "also-garbage")
	_, err := LoadOrGenerateVAPID(store)
	if err == nil {
		t.Fatalf("expected error on corrupt keys, got nil")
	}
	if !strings.Contains(err.Error(), "vapid") {
		t.Fatalf("expected vapid error, got %v", err)
	}
}

func TestVAPID_PublicKeyLength(t *testing.T) {
	store := newFakeKVStore()
	v, err := LoadOrGenerateVAPID(store)
	if err != nil {
		t.Fatalf("load/generate: %v", err)
	}
	// Base64url of 65-byte uncompressed P-256 point is 87 chars, no padding.
	if n := len(v.PublicKey()); n < 80 || n > 90 {
		t.Fatalf("unexpected public key length %d", n)
	}
}

func TestVAPID_RekeyClearsSubscriptions(t *testing.T) {
	store := newFakeKVStore()
	v1, err := LoadOrGenerateVAPID(store)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	old := v1.PublicKey()

	cleared := false
	v2, err := RekeyVAPID(store, func() error { cleared = true; return nil })
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if v2.PublicKey() == old {
		t.Fatalf("rekey did not produce a new key")
	}
	if !cleared {
		t.Fatalf("rekey did not call the clear-subscriptions callback")
	}
}

// --- fake kv store used by every notify test file ---

type fakeKVStore struct {
	data map[string]string
}

func newFakeKVStore() *fakeKVStore { return &fakeKVStore{data: map[string]string{}} }

func (f *fakeKVStore) set(k, v string) { f.data[k] = v }

func (f *fakeKVStore) GetKV(key string) (string, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeKVStore) SetKV(key, value string) error {
	f.data[key] = value
	return nil
}

// Compile-time check: fakeKVStore satisfies the KVStore interface defined in vapid.go.
var _ KVStore = (*fakeKVStore)(nil)
