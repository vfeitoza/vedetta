package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rvben/watchpost/internal/camera"
	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/recording"
	"github.com/rvben/watchpost/internal/storage"
)

// newTestServer creates a Server backed by an in-memory SQLite database
// and an empty camera manager (no real cameras).
func newTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()

	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("create in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mgr := camera.NewManager(nil, nil, nil, nil)
	rec := recording.New(config.RecordingConfig{
		Path: t.TempDir(),
	}, db)

	apiCfg := config.APIConfig{Host: "127.0.0.1", Port: 0}
	srv := New(apiCfg, db, mgr, rec)
	return srv, db
}

// seedEvent inserts a test event into the database.
func seedEvent(t *testing.T, db *storage.DB, id, cameraName, label string, score float32, ts time.Time) {
	t.Helper()
	err := db.SaveEvent(camera.Event{
		ID:         id,
		CameraName: cameraName,
		Label:      label,
		Score:      score,
		Box:        [4]int{10, 20, 100, 200},
		Timestamp:  ts,
	})
	if err != nil {
		t.Fatalf("seed event %s: %v", id, err)
	}
}

// seedSegment inserts a test segment into the database.
func seedSegment(t *testing.T, db *storage.DB, cam, path string, start, end time.Time, size int64) {
	t.Helper()
	err := db.SaveSegment(storage.SegmentRecord{
		Camera:    cam,
		Path:      path,
		StartTime: start,
		EndTime:   end,
		SizeBytes: size,
	})
	if err != nil {
		t.Fatalf("seed segment %s: %v", path, err)
	}
}

// --- Helper function unit tests ---

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
		{2199023255552, "2.0 TB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"front_door", "Front Door"},
		{"back_yard", "Back Yard"},
		{"garage", "Garage"},
		{"", ""},
		{"a_b_c", "A B C"},
		{"already", "Already"},
	}
	for _, tt := range tests {
		got := displayName(tt.input)
		if got != tt.want {
			t.Errorf("displayName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{60 * time.Minute, "1h"},
		{90 * time.Minute, "1h30m"},
		{2*time.Hour + 15*time.Minute, "2h15m"},
		{24 * time.Hour, "24h"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.input)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- JSON API handler tests ---

func TestHandleHealth(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/health: status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestHandleListCameras_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/cameras: status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string][]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body["cameras"]) != 0 {
		t.Errorf("cameras = %v, want empty", body["cameras"])
	}
}

func TestHandleListEvents_NoEvents(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/events: status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["events"]; !ok {
		t.Error("response missing 'events' key")
	}
}

func TestHandleListEvents_WithData(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)

	seedEvent(t, db, "evt-1", "front_door", "person", 0.95, now)
	seedEvent(t, db, "evt-2", "back_yard", "car", 0.80, now.Add(-time.Minute))
	seedEvent(t, db, "evt-3", "front_door", "dog", 0.70, now.Add(-2*time.Minute))

	t.Run("unfiltered returns all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		var body struct {
			Events []camera.Event `json:"events"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Events) != 3 {
			t.Errorf("got %d events, want 3", len(body.Events))
		}
	})

	t.Run("filter by camera", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?camera=front_door", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		var body struct {
			Events []camera.Event `json:"events"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Events) != 2 {
			t.Errorf("got %d events, want 2", len(body.Events))
		}
		for _, e := range body.Events {
			if e.CameraName != "front_door" {
				t.Errorf("event camera = %q, want %q", e.CameraName, "front_door")
			}
		}
	})

	t.Run("filter by label", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?label=car", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		var body struct {
			Events []camera.Event `json:"events"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Events) != 1 {
			t.Errorf("got %d events, want 1", len(body.Events))
		}
	})

	t.Run("limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?limit=2", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		var body struct {
			Events []camera.Event `json:"events"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Events) != 2 {
			t.Errorf("got %d events, want 2", len(body.Events))
		}
	})
}

func TestHandleGetEvent(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "evt-42", "garage", "person", 0.99, now)

	t.Run("found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events/evt-42", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var event camera.Event
		if err := json.NewDecoder(w.Body).Decode(&event); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if event.ID != "evt-42" {
			t.Errorf("event ID = %q, want %q", event.ID, "evt-42")
		}
		if event.Label != "person" {
			t.Errorf("event label = %q, want %q", event.Label, "person")
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events/nonexistent", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}

		var body map[string]string
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["error"] != "event not found" {
			t.Errorf("error = %q, want %q", body["error"], "event not found")
		}
	})
}

func TestHandleEventSnapshot_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/events/no-such-id/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleEventClip_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/events/no-such-id/clip", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleEventSnapshot_NoPath(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	// Event exists but has no snapshot_path
	seedEvent(t, db, "evt-nosnap", "cam1", "person", 0.9, now)

	req := httptest.NewRequest(http.MethodGet, "/api/events/evt-nosnap/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleEventClip_NoPath(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	// Event exists but has no clip_path
	seedEvent(t, db, "evt-noclip", "cam1", "person", 0.9, now)

	req := httptest.NewRequest(http.MethodGet, "/api/events/evt-noclip/clip", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSnapshot_CameraNotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cameras/nonexistent/snapshot", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "camera not found" {
		t.Errorf("error = %q, want %q", body["error"], "camera not found")
	}
}

func TestHandleSystemAPI(t *testing.T) {
	srv, db := newTestServer(t)

	// Seed some segments to have storage data
	now := time.Now().UTC()
	seedSegment(t, db, "cam1", "/tmp/seg1.mp4", now.Add(-10*time.Minute), now, 1048576)

	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body["version"] != "0.1.0" {
		t.Errorf("version = %v, want %q", body["version"], "0.1.0")
	}
	if body["hwaccel"] != "none" {
		t.Errorf("hwaccel = %v, want %q", body["hwaccel"], "none")
	}
	if body["cameras"].(float64) != 0 {
		t.Errorf("cameras = %v, want 0", body["cameras"])
	}
	if _, ok := body["uptime"]; !ok {
		t.Error("response missing 'uptime' key")
	}
	if _, ok := body["storage"]; !ok {
		t.Error("response missing 'storage' key")
	}
	storageBytes := body["storage_bytes"].(float64)
	if storageBytes != 1048576 {
		t.Errorf("storage_bytes = %v, want 1048576", storageBytes)
	}
}

// --- HTML partial handler tests ---

func TestHandleDashboardStatsPartial(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC()
	seedEvent(t, db, "today-1", "cam1", "person", 0.9, now)
	seedSegment(t, db, "cam1", "/tmp/s1.mp4", now.Add(-5*time.Minute), now, 2048)

	req := httptest.NewRequest(http.MethodGet, "/partials/dashboard-stats", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/html" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/html")
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("response body is empty")
	}
	// The dashboard stats template includes stat-card divs
	if !contains(body, "stat-card") {
		t.Error("response missing stat-card class")
	}
	if !contains(body, "Cameras") {
		t.Error("response missing Cameras label")
	}
	if !contains(body, "Events Today") {
		t.Error("response missing Events Today label")
	}
	if !contains(body, "Storage") {
		t.Error("response missing Storage label")
	}
}

func TestHandleCameraGridPartial_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/partials/camera-grid", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/html" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/html")
	}
	// No cameras means empty body (template iterates over empty slice)
	body := w.Body.String()
	if body != "" {
		t.Errorf("expected empty body for no cameras, got %q", body)
	}
}

func TestHandleEventsGalleryPartial_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/partials/events-gallery", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !contains(body, "No events recorded yet") {
		t.Errorf("expected empty state message, got %q", body)
	}
}

func TestHandleEventsGalleryPartial_WithEvents(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "gal-1", "front_door", "person", 0.85, now)

	req := httptest.NewRequest(http.MethodGet, "/partials/events-gallery", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !contains(body, "event-card") {
		t.Error("response missing event-card class")
	}
	if !contains(body, "person") {
		t.Error("response missing label 'person'")
	}
	if !contains(body, "front_door") {
		t.Error("response missing camera name 'front_door'")
	}
}

func TestHandleEventsGalleryPartial_Filters(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "f-1", "cam_a", "person", 0.9, now)
	seedEvent(t, db, "f-2", "cam_b", "car", 0.8, now)

	req := httptest.NewRequest(http.MethodGet, "/partials/events-gallery?camera=cam_a", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	body := w.Body.String()
	if !contains(body, "cam_a") {
		t.Error("filtered response should contain cam_a")
	}
	if contains(body, "cam_b") {
		t.Error("filtered response should not contain cam_b")
	}
}

func TestHandleEventDetailPartial(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "detail-1", "garage", "cat", 0.77, now)

	t.Run("found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/partials/event/detail-1", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		body := w.Body.String()
		if !contains(body, "cat") {
			t.Error("response missing label 'cat'")
		}
		if !contains(body, "garage") {
			t.Error("response missing camera 'garage'")
		}
		if !contains(body, "detail-1") {
			t.Error("response missing event ID")
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/partials/event/nonexistent", nil)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestHandleSystemStatusPartial(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/partials/system-status", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/html" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/html")
	}
	body := w.Body.String()
	if !contains(body, "cameras") {
		t.Error("response missing 'cameras' text")
	}
	if !contains(body, "online") {
		t.Error("response missing 'online' text")
	}
}

func TestHandleSystemPartial(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC()
	seedSegment(t, db, "cam1", "/tmp/sys-seg.mp4", now.Add(-5*time.Minute), now, 5242880)

	req := httptest.NewRequest(http.MethodGet, "/partials/system", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/html" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/html")
	}
	body := w.Body.String()
	if !contains(body, "System Info") {
		t.Error("response missing 'System Info' header")
	}
	if !contains(body, "0.1.0") {
		t.Error("response missing version")
	}
	if !contains(body, "Storage") {
		t.Error("response missing 'Storage' section")
	}
	if !contains(body, "5.0 MB") {
		t.Errorf("response missing formatted storage value '5.0 MB', body: %s", body)
	}
}

func TestHandleRecordingsPartial_NoCameras(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/partials/recordings", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !contains(body, "No cameras configured") {
		t.Errorf("expected empty state for no cameras, got %q", body)
	}
}

// --- Edge case tests ---

func TestHandleListEvents_InvalidLimit(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "lim-1", "cam1", "person", 0.9, now)

	// Invalid limit should fall back to default (50)
	req := httptest.NewRequest(http.MethodGet, "/api/events?limit=abc", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body struct {
		Events []camera.Event `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 1 {
		t.Errorf("got %d events, want 1", len(body.Events))
	}
}

func TestHandleListEvents_ZeroLimit(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "z-1", "cam1", "person", 0.9, now)

	// Zero limit should fall back to default (50) since parsed <= 0
	req := httptest.NewRequest(http.MethodGet, "/api/events?limit=0", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListEvents_NegativeLimit(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedEvent(t, db, "neg-1", "cam1", "person", 0.9, now)

	req := httptest.NewRequest(http.MethodGet, "/api/events?limit=-5", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestWriteJSON_ContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"key": "value"})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestHandleListEvents_CombinedFilters(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)

	seedEvent(t, db, "cf-1", "front_door", "person", 0.9, now)
	seedEvent(t, db, "cf-2", "front_door", "car", 0.8, now.Add(-time.Minute))
	seedEvent(t, db, "cf-3", "back_yard", "person", 0.7, now.Add(-2*time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/api/events?camera=front_door&label=person", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var body struct {
		Events []camera.Event `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 1 {
		t.Errorf("got %d events, want 1", len(body.Events))
	}
	if len(body.Events) > 0 && body.Events[0].ID != "cf-1" {
		t.Errorf("event ID = %q, want %q", body.Events[0].ID, "cf-1")
	}
}

func TestHandleListEvents_OrderedByTimestampDesc(t *testing.T) {
	srv, db := newTestServer(t)
	now := time.Now().UTC().Truncate(time.Second)

	seedEvent(t, db, "ord-old", "cam1", "person", 0.9, now.Add(-10*time.Minute))
	seedEvent(t, db, "ord-new", "cam1", "person", 0.8, now)
	seedEvent(t, db, "ord-mid", "cam1", "person", 0.7, now.Add(-5*time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	var body struct {
		Events []camera.Event `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 3 {
		t.Fatalf("got %d events, want 3", len(body.Events))
	}
	if body.Events[0].ID != "ord-new" {
		t.Errorf("first event = %q, want %q (most recent)", body.Events[0].ID, "ord-new")
	}
	if body.Events[1].ID != "ord-mid" {
		t.Errorf("second event = %q, want %q", body.Events[1].ID, "ord-mid")
	}
	if body.Events[2].ID != "ord-old" {
		t.Errorf("third event = %q, want %q (oldest)", body.Events[2].ID, "ord-old")
	}
}

// contains checks if substr is present in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
