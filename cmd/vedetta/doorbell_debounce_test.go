package main

import (
	"testing"
	"time"
)

func TestDoorbellDebouncer(t *testing.T) {
	d := newDoorbellDebouncer()
	base := time.Unix(1_000_000, 0)
	if !d.allow("front_door", base, 10*time.Second) {
		t.Fatal("first press should be allowed")
	}
	if d.allow("front_door", base.Add(5*time.Second), 10*time.Second) {
		t.Error("press 5s later within 10s window should be debounced")
	}
	if !d.allow("front_door", base.Add(11*time.Second), 10*time.Second) {
		t.Error("press after the window should be allowed")
	}
	if !d.allow("garage", base, 0) {
		t.Fatal("first garage press allowed")
	}
	if !d.allow("garage", base, 0) {
		t.Error("zero window must never debounce")
	}
}
