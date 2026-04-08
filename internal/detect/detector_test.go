package detect

import (
	"testing"

	"github.com/rvben/vedetta/internal/config"
)

func TestDetector_SetScoreThreshold(t *testing.T) {
	d := &Detector{
		config: config.DetectConfig{ScoreThreshold: 0.5},
	}

	d.SetScoreThreshold(0.8)
	if got := d.ScoreThreshold(); got != 0.8 {
		t.Errorf("expected 0.8, got %v", got)
	}
}

func TestDetector_SetLabels(t *testing.T) {
	d := &Detector{}

	if d.Labels() != nil {
		t.Fatal("initial Labels() should be nil")
	}

	d.SetLabels([]string{"person", "car"})
	labels := d.Labels()
	if len(labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(labels))
	}
	// Check via the filter
	d.mu.Lock()
	if !d.labelAllowed["person"] {
		t.Error("person should be allowed")
	}
	if d.labelAllowed["dog"] {
		t.Error("dog should not be allowed")
	}
	d.mu.Unlock()

	// Reset to allow all
	d.SetLabels(nil)
	if d.Labels() != nil {
		t.Error("nil labels should reset to allow all")
	}
}
