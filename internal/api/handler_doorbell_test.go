package api

import (
	"image"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rvben/vedetta/internal/camera"
)

func TestPressDoorbell_Accepted(t *testing.T) {
	mgr := camera.NewManagerForTest()
	cam := camera.NewTestCamera("front_door")
	cam.SetTestFrame(image.NewRGBA(image.Rect(0, 0, 16, 16)))
	mgr.RegisterForTest(cam)

	s := &Server{cameras: mgr}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/cameras/front_door/doorbell", nil)
	s.PressDoorbell(rec, req, "front_door")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

func TestPressDoorbell_UnknownCamera(t *testing.T) {
	s := &Server{cameras: camera.NewManagerForTest()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/cameras/none/doorbell", nil)
	s.PressDoorbell(rec, req, "none")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
