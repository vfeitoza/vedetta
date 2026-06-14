package config

import "testing"

func TestEffectiveDoorbellClipSeconds(t *testing.T) {
	global := 15
	c := CameraConfig{}
	if got := c.EffectiveDoorbellClipSeconds(global); got != 15 {
		t.Errorf("no override: got %d, want 15", got)
	}
	override := 20
	c.Doorbell.ClipSeconds = &override
	if got := c.EffectiveDoorbellClipSeconds(global); got != 20 {
		t.Errorf("override: got %d, want 20", got)
	}
}

func TestEffectiveDoorbellDebounceSeconds(t *testing.T) {
	c := CameraConfig{}
	if got := c.EffectiveDoorbellDebounceSeconds(10); got != 10 {
		t.Errorf("no override: got %d, want 10", got)
	}
	zero := 0
	c.Doorbell.DebounceSeconds = &zero // explicit 0 = no debounce, must be honored
	if got := c.EffectiveDoorbellDebounceSeconds(10); got != 0 {
		t.Errorf("explicit zero override: got %d, want 0", got)
	}
}

func TestDoorbellDefaultsApplied(t *testing.T) {
	cfg := Defaults()
	if cfg.Doorbell.ClipSeconds != 15 {
		t.Errorf("default clip_seconds = %d, want 15", cfg.Doorbell.ClipSeconds)
	}
	if cfg.Doorbell.DebounceSeconds != 10 {
		t.Errorf("default debounce_seconds = %d, want 10", cfg.Doorbell.DebounceSeconds)
	}
}
