package camera

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFireDoorbellWebhook(t *testing.T) {
	got := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- body
	}))
	defer srv.Close()

	fireDoorbellWebhook(srv.URL, "front_door", "front_door-doorbell-123", "Alice")
	select {
	case body := <-got:
		if body["camera"] != "front_door" || body["event_id"] != "front_door-doorbell-123" {
			t.Errorf("unexpected body: %v", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not called")
	}
}

func TestFireDoorbellWebhook_EmptyURLNoop(t *testing.T) {
	fireDoorbellWebhook("", "c", "e", "") // must not panic or block
}
