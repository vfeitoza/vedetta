package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestPostStorageDelete_DryRun(t *testing.T) {
	s, db := newTestServer(t)

	camDir := filepath.Join(s.recorder.Path(), "cam-a")
	if err := os.MkdirAll(camDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	paths := []string{}
	for i := 0; i < 3; i++ {
		start := now.AddDate(0, 0, -10-i)
		end := start.Add(time.Hour)
		p := filepath.Join(camDir, fmt.Sprintf("seg-%d.mp4", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		seedSegment(t, db, "cam-a", p, start, end, 1024)
		paths = append(paths, p)
	}

	body := `{"target":"segments","camera":"cam-a","older_than_days":7}`
	req := httptest.NewRequest(http.MethodPost, "/api/storage/delete?dry_run=true",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	dryRun := true
	s.PostStorageDelete(w, req, PostStorageDeleteParams{DryRun: &dryRun})

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if int(res["segments"].(float64)) != 3 {
		t.Errorf("dry-run reported %v segments, want 3", res["segments"])
	}
	// Files must still exist after a dry-run.
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dry-run removed %s: %v", p, err)
		}
	}
}

func TestPostStorageDelete_409WhenLockHeld(t *testing.T) {
	s, _ := newTestServer(t)
	s.recorder.LockForTest()
	defer s.recorder.UnlockForTest()

	body := `{"target":"segments","camera":"cam-a","older_than_days":7}`
	req := httptest.NewRequest(http.MethodPost, "/api/storage/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.PostStorageDelete(w, req, PostStorageDeleteParams{})

	if w.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After=%q, want 5", got)
	}
}
