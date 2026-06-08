package camera

import (
	"context"
	"testing"
)

func TestRunningCameraDetectURLs(t *testing.T) {
	camA := NewTestCamera("a")
	camA.config.URL = "rtsp://192.0.2.60:554/a_sub"
	camB := NewTestCamera("b")
	camB.config.URL = "rtsp://192.0.2.61:554/b_sub"

	m := &Manager{
		cameras:     map[string]*Camera{"a": camA, "b": camB},
		cancelFuncs: map[string]context.CancelFunc{"a": func() {}}, // only "a" running
		order:       []string{"a", "b"},
	}

	got := m.RunningCameraDetectURLs()
	if len(got) != 1 || got[0] != "rtsp://192.0.2.60:554/a_sub" {
		t.Fatalf("expected only running camera a's DetectURL, got %v", got)
	}
}
