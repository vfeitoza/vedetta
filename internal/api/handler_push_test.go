package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rvben/vedetta/internal/storage"
)

// Valid Web Push subscription keys for tests. p256dh decodes to exactly 65
// bytes (uncompressed P-256 point); auth decodes to exactly 16 bytes.
const (
	testP256dh = "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM"
	testAuth   = "tBHItJI5svbpez7KI4CCXg"
	// testEndpoint uses a public IP literal so ValidateSubscriptionEndpoint
	// succeeds without depending on DNS during the test run.
	testEndpoint      = "https://8.8.8.8/push/abc"
	testEndpointOther = "https://8.8.4.4/push/xyz"
)

func TestCreatePushSubscription_Valid(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	body := map[string]any{
		"endpoint":  testEndpoint,
		"userAgent": "Firefox on Linux",
		"keys":      map[string]string{"p256dh": testP256dh, "auth": testAuth},
	}
	jb, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(jb))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]int64
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["id"] <= 0 {
		t.Errorf("expected positive id, got %d", out["id"])
	}
}

func TestCreatePushSubscription_RejectsHTTP(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	body := `{"endpoint":"http://8.8.8.8/x","keys":{"p256dh":"` + testP256dh + `","auth":"` + testAuth + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePushSubscription_RejectsPrivateEndpoint(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	body := `{"endpoint":"https://10.0.0.1/x","keys":{"p256dh":"` + testP256dh + `","auth":"` + testAuth + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (SSRF guard), got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePushSubscription_RejectsInvalidKeys(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	body := `{"endpoint":"` + testEndpoint + `","keys":{"p256dh":"shortkey","auth":"` + testAuth + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid keys, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePushSubscription_RebindDifferentUserReturns409(t *testing.T) {
	srv, db := newTestServerWithUsers(t, "alice", "bob")

	if _, err := db.SavePushSubscription(storage.PushSubscription{
		Username: "alice", Endpoint: testEndpoint, P256dh: testP256dh, Auth: testAuth,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := map[string]any{
		"endpoint": testEndpoint,
		"keys":     map[string]string{"p256dh": testP256dh, "auth": testAuth},
	}
	jb, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(jb))
	req = withSessionPrincipal(req, "bob")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePushSubscription_SameUserRebindIsOK(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")

	firstID, err := db.SavePushSubscription(storage.PushSubscription{
		Username: "alice", Endpoint: testEndpoint, P256dh: testP256dh, Auth: testAuth,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := map[string]any{
		"endpoint":  testEndpoint,
		"userAgent": "Firefox rebind",
		"keys":      map[string]string{"p256dh": testP256dh, "auth": testAuth},
	}
	jb, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(jb))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on same-user rebind, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]int64
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out["id"] != firstID {
		t.Errorf("expected same id %d on rebind, got %d", firstID, out["id"])
	}
}

func TestCreatePushSubscription_InvalidJSON(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader("not json"))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPushEndpoints_RejectTokenPrincipal(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	body := `{"endpoint":"` + testEndpoint + `","keys":{"p256dh":"` + testP256dh + `","auth":"` + testAuth + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body))
	req = withTokenPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.CreatePushSubscription(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for token principal, got %d", rec.Code)
	}
}

func TestPushEndpoints_RejectMissingPrincipal(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	req := httptest.NewRequest(http.MethodGet, "/api/push/subscriptions", nil)
	// No principal injected.
	rec := httptest.NewRecorder()

	srv.ListPushSubscriptions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without principal, got %d", rec.Code)
	}
}

func TestListPushSubscriptions_OnlyOwnSubs(t *testing.T) {
	srv, db := newTestServerWithUsers(t, "alice", "bob")

	if _, err := db.SavePushSubscription(storage.PushSubscription{
		Username: "alice", Endpoint: testEndpoint, P256dh: testP256dh, Auth: testAuth, UserAgent: "Firefox",
	}); err != nil {
		t.Fatalf("seed alice: %v", err)
	}
	if _, err := db.SavePushSubscription(storage.PushSubscription{
		Username: "bob", Endpoint: testEndpointOther, P256dh: testP256dh, Auth: testAuth, UserAgent: "Chrome",
	}); err != nil {
		t.Fatalf("seed bob: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/push/subscriptions", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.ListPushSubscriptions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(out))
	}
	if out[0]["userAgent"] != "Firefox" {
		t.Errorf("expected Firefox, got %v", out[0]["userAgent"])
	}
	// Endpoint and keys must not leak in the list response.
	if _, ok := out[0]["endpoint"]; ok {
		t.Error("endpoint should not appear in list response")
	}
	if _, ok := out[0]["keys"]; ok {
		t.Error("keys should not appear in list response")
	}
}

func TestDeletePushSubscription_OwnSub(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")

	id, err := db.SavePushSubscription(storage.PushSubscription{
		Username: "alice", Endpoint: testEndpoint, P256dh: testP256dh, Auth: testAuth,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions/"+strconv.FormatInt(id, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.DeletePushSubscription(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	remaining, _ := db.ListPushSubscriptionsByUser("alice")
	if len(remaining) != 0 {
		t.Errorf("expected 0 subs after delete, got %d", len(remaining))
	}
}

func TestDeletePushSubscription_OtherUserReturns404(t *testing.T) {
	srv, db := newTestServerWithUsers(t, "alice", "bob")

	id, err := db.SavePushSubscription(storage.PushSubscription{
		Username: "alice", Endpoint: testEndpoint, P256dh: testP256dh, Auth: testAuth,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions/"+strconv.FormatInt(id, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	req = withSessionPrincipal(req, "bob")
	rec := httptest.NewRecorder()

	srv.DeletePushSubscription(rec, req)

	// 404, not 403 — we deliberately refuse to distinguish "not yours"
	// from "doesn't exist" so that bob can't enumerate alice's sub IDs.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	remaining, _ := db.ListPushSubscriptionsByUser("alice")
	if len(remaining) != 1 {
		t.Errorf("alice's sub should be untouched, got %d remaining", len(remaining))
	}
}

func TestDeletePushSubscription_NonExistent(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions/9999", nil)
	req.SetPathValue("id", "9999")
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.DeletePushSubscription(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestDeletePushSubscription_InvalidID(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions/notanumber", nil)
	req.SetPathValue("id", "notanumber")
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.DeletePushSubscription(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetPushPrefs_Defaults(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")
	srv.SetCameraNames([]string{"front_door", "backyard"})

	req := httptest.NewRequest(http.MethodGet, "/api/push/prefs", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.GetPushPrefs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out prefsResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Muted {
		t.Error("default muted should be false")
	}
	if out.CooldownSeconds != 180 {
		t.Errorf("default cooldown should be 180, got %d", out.CooldownSeconds)
	}
	if _, ok := out.Cameras["front_door"]; !ok {
		t.Error("expected front_door in cameras map")
	}
	if !out.Cameras["front_door"]["person"] {
		t.Error("person should default to enabled")
	}
}

func TestGetPushPrefs_ReflectsDisabledRows(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")
	srv.SetCameraNames([]string{"front_door", "backyard"})

	// Disable person on front_door, and disable all classes on backyard via "*".
	if err := db.SetNotificationPref("alice", "front_door", "person", false); err != nil {
		t.Fatalf("seed front_door/person: %v", err)
	}
	if err := db.SetNotificationPref("alice", "backyard", "*", false); err != nil {
		t.Fatalf("seed backyard/*: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/push/prefs", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.GetPushPrefs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var out prefsResponse
	_ = json.NewDecoder(rec.Body).Decode(&out)

	if out.Cameras["front_door"]["person"] {
		t.Error("front_door/person should be disabled")
	}
	if !out.Cameras["front_door"]["car"] {
		t.Error("front_door/car should remain enabled")
	}
	for class, enabled := range out.Cameras["backyard"] {
		if enabled {
			t.Errorf("backyard/%s should be disabled via wildcard", class)
		}
	}
}

func TestGetPushPrefs_Muted(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")
	srv.SetCameraNames([]string{"front_door"})
	_ = db.SetKV("notify:alice:muted", "1")
	_ = db.SetKV("notify:alice:cooldown_seconds", "600")

	req := httptest.NewRequest(http.MethodGet, "/api/push/prefs", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.GetPushPrefs(rec, req)

	var out prefsResponse
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if !out.Muted {
		t.Error("expected muted=true")
	}
	if out.CooldownSeconds != 600 {
		t.Errorf("expected cooldown=600, got %d", out.CooldownSeconds)
	}
}

func TestPutPushPrefs_RoundTrip(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")
	srv.SetCameraNames([]string{"front_door"})

	body := prefsResponse{
		Muted:           true,
		CooldownSeconds: 300,
		Cameras: map[string]map[string]bool{
			"front_door": {"person": false, "car": true},
		},
	}
	jb, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/push/prefs", bytes.NewReader(jb))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.PutPushPrefs(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}

	muted, _, _ := db.GetKV("notify:alice:muted")
	if muted != "1" {
		t.Errorf("muted kv = %q, want 1", muted)
	}
	cd, _, _ := db.GetKV("notify:alice:cooldown_seconds")
	if cd != "300" {
		t.Errorf("cooldown kv = %q, want 300", cd)
	}
	enabled, _ := db.IsNotificationEnabled("alice", "front_door", "person")
	if enabled {
		t.Error("front_door/person should be disabled in DB")
	}
	carEnabled, _ := db.IsNotificationEnabled("alice", "front_door", "car")
	if !carEnabled {
		t.Error("front_door/car should still be enabled in DB")
	}
}

func TestPutPushPrefs_Unmute(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")
	srv.SetCameraNames([]string{"front_door"})
	_ = db.SetKV("notify:alice:muted", "1")

	body := prefsResponse{Muted: false, CooldownSeconds: 180}
	jb, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/push/prefs", bytes.NewReader(jb))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.PutPushPrefs(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	muted, _, _ := db.GetKV("notify:alice:muted")
	if muted != "0" {
		t.Errorf("muted kv = %q, want 0", muted)
	}
}

func TestPutPushPrefs_InvalidJSON(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	req := httptest.NewRequest(http.MethodPut, "/api/push/prefs", strings.NewReader("{bad"))
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.PutPushPrefs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetVAPIDPublicKey_NoNotifier(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")

	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid-public-key", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.GetVAPIDPublicKey(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when notifier is nil, got %d", rec.Code)
	}
}

func TestTestPush_NoNotifier(t *testing.T) {
	srv, _ := newTestServerWithUser(t, "alice")
	srv.SetCameraNames([]string{"front_door"})

	req := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.TestPush(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when notifier is nil, got %d", rec.Code)
	}
}

func TestTestPush_NoCameras(t *testing.T) {
	srv, db := newTestServerWithUser(t, "alice")
	// Install a no-op notifier so we reach the camera check.
	srv.SetNotifier(newNoopDispatcherForTest(t, db))

	req := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	req = withSessionPrincipal(req, "alice")
	rec := httptest.NewRecorder()

	srv.TestPush(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 with no cameras, got %d body=%s", rec.Code, rec.Body.String())
	}
}
