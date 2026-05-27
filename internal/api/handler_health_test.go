package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestGetHealth_RecompressionClipsRecompressed(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	srv.GetHealth(w, req)

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}

	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("health response missing checks map")
	}
	storageMap, ok := checks["storage"].(map[string]any)
	if !ok {
		t.Fatalf("health checks missing storage map")
	}
	recompression, ok := storageMap["recompression"].(map[string]any)
	if !ok {
		t.Fatalf("health storage missing recompression map")
	}
	v, ok := recompression["clips_recompressed"]
	if !ok {
		t.Fatal("health storage.recompression missing clips_recompressed")
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("clips_recompressed not a JSON number: %T", v)
	}
	if n != 0 {
		t.Errorf("clips_recompressed = %v, want 0 for unseeded recorder", n)
	}
}
