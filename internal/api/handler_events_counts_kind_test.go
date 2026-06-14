package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func TestGetEventCounts_KindFilter(t *testing.T) {
	s, db := newTestServer(t)

	// Seed 1 object event on cam-a and 2 doorbell rings on cam-b and cam-c.
	if err := db.SaveEvent(camera.Event{
		ID:         "obj1",
		CameraName: "cam-a",
		Label:      "person",
		Kind:       camera.EventKindObject,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed object event: %v", err)
	}
	if err := db.SaveEvent(camera.Event{
		ID:         "ring1",
		CameraName: "cam-b",
		Label:      "doorbell",
		Kind:       camera.EventKindDoorbell,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed doorbell ring 1: %v", err)
	}
	if err := db.SaveEvent(camera.Event{
		ID:         "ring2",
		CameraName: "cam-c",
		Label:      "doorbell",
		Kind:       camera.EventKindDoorbell,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed doorbell ring 2: %v", err)
	}

	t.Run("kind=doorbell returns only doorbell counts", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/events/counts?kind=doorbell", nil)
		kind := "doorbell"
		s.GetEventCounts(rec, req, GetEventCountsParams{Kind: &kind})

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		total := int(resp["total"].(float64))
		if total != 2 {
			t.Errorf("total = %d, want 2 (doorbell only)", total)
		}

		byCamera, ok := resp["by_camera"].(map[string]any)
		if !ok {
			t.Fatalf("by_camera missing or wrong type: %v", resp["by_camera"])
		}
		if _, hasObjCam := byCamera["cam-a"]; hasObjCam {
			t.Error("by_camera must not include cam-a (object-only camera) when filtering by doorbell")
		}
		if int(byCamera["cam-b"].(float64)) != 1 {
			t.Errorf("cam-b count = %v, want 1", byCamera["cam-b"])
		}
		if int(byCamera["cam-c"].(float64)) != 1 {
			t.Errorf("cam-c count = %v, want 1", byCamera["cam-c"])
		}
	})

	t.Run("no kind returns all-kinds counts (no regression)", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/events/counts", nil)
		s.GetEventCounts(rec, req, GetEventCountsParams{})

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		total := int(resp["total"].(float64))
		if total != 3 {
			t.Errorf("total = %d, want 3 (all kinds)", total)
		}

		byCamera, ok := resp["by_camera"].(map[string]any)
		if !ok {
			t.Fatalf("by_camera missing or wrong type: %v", resp["by_camera"])
		}
		if _, hasCamA := byCamera["cam-a"]; !hasCamA {
			t.Error("by_camera must include cam-a when no kind filter")
		}
	})
}
