package detect

import "testing"

func TestMotionScore_Identical(t *testing.T) {
	frame := make([]byte, 300) // 100 pixels * 3 channels
	for i := range frame {
		frame[i] = 128
	}

	score := MotionScore(frame, frame)
	if score != 0 {
		t.Errorf("expected 0 for identical frames, got %f", score)
	}
}

func TestMotionScore_CompletelyDifferent(t *testing.T) {
	prev := make([]byte, 300)
	curr := make([]byte, 300)
	for i := range curr {
		curr[i] = 255
	}

	score := MotionScore(prev, curr)
	if score < 0.99 {
		t.Errorf("expected ~1.0 for max difference, got %f", score)
	}
}

func TestMotionScore_Empty(t *testing.T) {
	score := MotionScore(nil, nil)
	if score != 0 {
		t.Errorf("expected 0 for empty frames, got %f", score)
	}
}

func TestMotionScore_DifferentLengths(t *testing.T) {
	a := make([]byte, 300)
	b := make([]byte, 600)
	score := MotionScore(a, b)
	if score != 0 {
		t.Errorf("expected 0 for mismatched frames, got %f", score)
	}
}
