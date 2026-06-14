package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func TestListEvents_KindFilter(t *testing.T) {
	s, db := newTestServer(t)

	if err := db.SaveEvent(camera.Event{
		ID:         "o1",
		CameraName: "c",
		Label:      "person",
		Kind:       camera.EventKindObject,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed object event: %v", err)
	}
	if err := db.SaveEvent(camera.Event{
		ID:         "d1",
		CameraName: "c",
		Label:      "doorbell",
		Kind:       camera.EventKindDoorbell,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed doorbell event: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events?kind=doorbell", nil)
	kind := "doorbell"
	s.ListEvents(rec, req, ListEventsParams{Kind: &kind})

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, body)
	}
	if !strings.Contains(body, "d1") {
		t.Errorf("kind=doorbell: response does not contain doorbell event d1; body: %s", body)
	}
	if strings.Contains(body, "o1") {
		t.Errorf("kind=doorbell: response must not contain object event o1; body: %s", body)
	}
}

func TestListEvents_EmptyKindMatchesAll(t *testing.T) {
	s, db := newTestServer(t)

	if err := db.SaveEvent(camera.Event{
		ID:         "o1",
		CameraName: "c",
		Label:      "person",
		Kind:       camera.EventKindObject,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed object event: %v", err)
	}
	if err := db.SaveEvent(camera.Event{
		ID:         "d1",
		CameraName: "c",
		Label:      "doorbell",
		Kind:       camera.EventKindDoorbell,
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("seed doorbell event: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	s.ListEvents(rec, req, ListEventsParams{})

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, body)
	}
	if !strings.Contains(body, "d1") || !strings.Contains(body, "o1") {
		t.Errorf("no kind filter: expected both events in response; body: %s", body)
	}
}
