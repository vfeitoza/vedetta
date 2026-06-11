package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

// timelineSegments fetches the timeline endpoint and returns the segments array.
func timelineSegments(t *testing.T, srv *Server, url string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d: %s", url, w.Code, w.Body.String())
	}
	var body struct {
		Segments []map[string]any `json:"segments"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode timeline response: %v", err)
	}
	return body.Segments
}

// A recording made shortly after local midnight in a UTC+2 timezone belongs to
// that local calendar day, even though it falls on the previous UTC day. The
// tz parameter must shift the day boundaries accordingly.
func TestGetCameraTimeline_TimezoneDayBoundaries(t *testing.T) {
	srv, db := newTestServer(t)
	srv.cameras.RegisterForTest(camera.NewTestCamera("cam1"))

	// 23:30 UTC June 10 = 01:30 June 11 in Europe/Amsterdam (CEST, UTC+2).
	start := time.Date(2026, 6, 10, 23, 30, 0, 0, time.UTC)
	seedSegment(t, db, "cam1", "/night.mp4", start, start.Add(10*time.Minute), 1000)

	// On the user's June 11 calendar the recording must appear.
	segs := timelineSegments(t, srv, "/api/cameras/cam1/timeline?date=2026-06-11&tz=Europe/Amsterdam")
	if len(segs) != 1 {
		t.Errorf("local June 11: got %d segments, want 1", len(segs))
	}

	// And it must NOT appear on the local June 10 view (it is 01:30 on the 11th).
	segs = timelineSegments(t, srv, "/api/cameras/cam1/timeline?date=2026-06-10&tz=Europe/Amsterdam")
	if len(segs) != 0 {
		t.Errorf("local June 10: got %d segments, want 0", len(segs))
	}

	// Without tz the day is interpreted as UTC, where the segment is on June 10.
	segs = timelineSegments(t, srv, "/api/cameras/cam1/timeline?date=2026-06-10")
	if len(segs) != 1 {
		t.Errorf("UTC June 10: got %d segments, want 1", len(segs))
	}
}

// A segment spanning local midnight must show up on both adjacent days
// (overlap semantics), so coverage near midnight is never invisible.
func TestGetCameraTimeline_SegmentSpanningMidnight(t *testing.T) {
	srv, db := newTestServer(t)
	srv.cameras.RegisterForTest(camera.NewTestCamera("cam1"))

	// 23:55 June 10 to 00:05 June 11 (UTC).
	start := time.Date(2026, 6, 10, 23, 55, 0, 0, time.UTC)
	seedSegment(t, db, "cam1", "/span.mp4", start, start.Add(10*time.Minute), 1000)

	for _, date := range []string{"2026-06-10", "2026-06-11"} {
		segs := timelineSegments(t, srv, "/api/cameras/cam1/timeline?date="+date)
		if len(segs) != 1 {
			t.Errorf("date %s: got %d segments, want 1 (spans midnight)", date, len(segs))
		}
	}
}

// An unknown timezone name must degrade gracefully to UTC, not error.
func TestGetCameraTimeline_UnknownTimezoneFallsBackToUTC(t *testing.T) {
	srv, db := newTestServer(t)
	srv.cameras.RegisterForTest(camera.NewTestCamera("cam1"))

	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	seedSegment(t, db, "cam1", "/noon.mp4", start, start.Add(10*time.Minute), 1000)

	segs := timelineSegments(t, srv, "/api/cameras/cam1/timeline?date=2026-06-10&tz=Not/AZone")
	if len(segs) != 1 {
		t.Errorf("unknown tz: got %d segments, want 1 (UTC fallback)", len(segs))
	}
}
