package main

import (
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/config"
)

func TestDoorbellClipWindow(t *testing.T) {
	cfg := &config.Config{
		Doorbell: config.DoorbellDefaultsConfig{ClipSeconds: 15},
		Cameras: []config.CameraConfig{
			{Name: "front_door"},
			{Name: "garage"},
		},
	}
	override := 30
	cfg.Cameras[1].Doorbell.ClipSeconds = &override

	if got := doorbellClipWindow(cfg, "front_door"); got != 15*time.Second {
		t.Errorf("front_door window = %v, want 15s", got)
	}
	if got := doorbellClipWindow(cfg, "garage"); got != 30*time.Second {
		t.Errorf("garage window = %v, want 30s", got)
	}
	if got := doorbellClipWindow(cfg, "unknown"); got != 15*time.Second {
		t.Errorf("unknown window = %v, want 15s", got)
	}
}
