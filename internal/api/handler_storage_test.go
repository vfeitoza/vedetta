package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetStorage_ReturnsBreakdown(t *testing.T) {
	s, db := newTestServer(t)
	now := time.Now().UTC()
	seedSegment(t, db, "cam-a", "/tmp/cam-a/seg-1.mp4",
		now.Add(-time.Hour), now.Add(-30*time.Minute), 1024)

	req := httptest.NewRequest(http.MethodGet, "/api/storage", nil)
	w := httptest.NewRecorder()
	s.GetStorage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["recording"]; !ok {
		t.Errorf("response missing 'recording' key: %v", out)
	}
}
