package notify

import (
	"strings"
	"testing"
	"time"
)

func TestSnapshotSigner_SignAndVerify(t *testing.T) {
	store := newFakeKVStore()
	s, err := LoadOrGenerateSnapshotSigner(store)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fixedNow := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return fixedNow }

	path := s.Sign("front-t91-1712847123456")
	if !strings.HasPrefix(path, "/api/push/snapshot/") {
		t.Fatalf("unexpected path: %s", path)
	}

	// Parse path+query manually to mimic what the handler does.
	eventID, exp, sig := parseSignedSnapshotPath(t, path)
	if eventID != "front-t91-1712847123456" {
		t.Errorf("eventID = %q", eventID)
	}
	if err := s.Verify(eventID, exp, sig); err != nil {
		t.Errorf("verify valid: %v", err)
	}

	// Tamper with event ID.
	if err := s.Verify("other-event", exp, sig); err == nil {
		t.Errorf("verify should fail for wrong event")
	}

	// Tamper with expiry.
	if err := s.Verify(eventID, "9999999999", sig); err == nil {
		t.Errorf("verify should fail for wrong expiry")
	}

	// Tamper with signature.
	if err := s.Verify(eventID, exp, "AAAAAAAA"); err == nil {
		t.Errorf("verify should fail for wrong sig")
	}
}

func TestSnapshotSigner_Expired(t *testing.T) {
	store := newFakeKVStore()
	s, _ := LoadOrGenerateSnapshotSigner(store)
	fixedNow := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return fixedNow }

	path := s.Sign("evt-1")
	eventID, exp, sig := parseSignedSnapshotPath(t, path)

	// Advance clock past expiry.
	s.now = func() time.Time { return fixedNow.Add(48 * time.Hour) }
	if err := s.Verify(eventID, exp, sig); err == nil || err.Error() != "expired" {
		t.Errorf("expected 'expired', got %v", err)
	}
}

func TestSnapshotSigner_ReloadSameKey(t *testing.T) {
	store := newFakeKVStore()
	s1, _ := LoadOrGenerateSnapshotSigner(store)
	s2, _ := LoadOrGenerateSnapshotSigner(store)
	if string(s1.key) != string(s2.key) {
		t.Errorf("second load produced a different key")
	}
}

func parseSignedSnapshotPath(t *testing.T, path string) (eventID, exp, sig string) {
	t.Helper()
	// Format: /api/push/snapshot/<id>?e=<unix>&s=<b64>
	parts := strings.SplitN(path, "?", 2)
	if len(parts) != 2 {
		t.Fatalf("no query: %s", path)
	}
	eventID = strings.TrimPrefix(parts[0], "/api/push/snapshot/")
	for _, kv := range strings.Split(parts[1], "&") {
		if strings.HasPrefix(kv, "e=") {
			exp = strings.TrimPrefix(kv, "e=")
		} else if strings.HasPrefix(kv, "s=") {
			sig = strings.TrimPrefix(kv, "s=")
		}
	}
	return
}
