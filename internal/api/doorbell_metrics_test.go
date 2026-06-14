package api

import "testing"

func TestDoorbellMetrics(t *testing.T) {
	m := newDoorbellMetrics()
	m.recordPress("front_door")
	m.recordPress("front_door")
	if got := m.pressCounts()["front_door"]; got != 2 {
		t.Errorf("presses = %d, want 2", got)
	}
	if got := m.unansweredCount("front_door"); got != 2 {
		t.Errorf("unanswered = %d, want 2", got)
	}
	m.recordAnswer("front_door")
	if got := m.unansweredCount("front_door"); got != 1 {
		t.Errorf("unanswered after answer = %d, want 1", got)
	}
	m.recordAnswer("front_door")
	if _, present := m.unansweredCounts()["front_door"]; present {
		t.Error("zero unanswered should be omitted from the map")
	}
}
