package reid

import (
	"math"
	"testing"
)

func l2Norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestBestMatch_ReturnsMostSimilarAboveThreshold(t *testing.T) {
	embedding := []float32{1, 0, 0}
	candidates := []Candidate{
		{ID: 10, Centroid: []float32{0, 1, 0}}, // orthogonal, sim 0
		{ID: 20, Centroid: []float32{1, 0, 0}}, // identical, sim 1
	}
	id, sim := BestMatch(embedding, candidates, 0.5)
	if id != 20 {
		t.Errorf("id = %d, want 20", id)
	}
	if sim < 0.99 {
		t.Errorf("sim = %v, want ~1.0", sim)
	}
}

func TestBestMatch_BelowThresholdReturnsZero(t *testing.T) {
	embedding := []float32{1, 0, 0}
	candidates := []Candidate{
		{ID: 10, Centroid: []float32{0, 1, 0}}, // sim 0
	}
	id, sim := BestMatch(embedding, candidates, 0.5)
	if id != 0 || sim != 0 {
		t.Errorf("got (%d, %v), want (0, 0) when best is below threshold", id, sim)
	}
}

func TestBestMatch_SkipsIgnoredCandidate(t *testing.T) {
	embedding := []float32{1, 0, 0}
	candidates := []Candidate{
		{ID: 10, Centroid: []float32{1, 0, 0}, Ignore: true}, // perfect but ignored
		{ID: 20, Centroid: []float32{0, 1, 0}},               // sim 0
	}
	id, sim := BestMatch(embedding, candidates, 0.5)
	if id != 0 || sim != 0 {
		t.Errorf("got (%d, %v), want (0, 0): ignored candidate must not match", id, sim)
	}
}

func TestBestMatch_SkipsEmptyCentroid(t *testing.T) {
	embedding := []float32{1, 0, 0}
	candidates := []Candidate{
		{ID: 10, Centroid: nil},
		{ID: 20, Centroid: []float32{1, 0, 0}},
	}
	id, _ := BestMatch(embedding, candidates, 0.5)
	if id != 20 {
		t.Errorf("id = %d, want 20 (empty-centroid candidate skipped)", id)
	}
}

func TestBestMatch_NoCandidates(t *testing.T) {
	id, sim := BestMatch([]float32{1, 0}, nil, 0.5)
	if id != 0 || sim != 0 {
		t.Errorf("got (%d, %v), want (0, 0) for no candidates", id, sim)
	}
}

func TestBlendCentroid_EmptyOldReturnsNewUnchanged(t *testing.T) {
	newEmb := []float32{0.5, 0.5}
	got := BlendCentroid(nil, newEmb, 0.3)
	if len(got) != 2 || got[0] != 0.5 || got[1] != 0.5 {
		t.Errorf("got %v, want the new embedding unchanged", got)
	}
}

func TestBlendCentroid_LengthMismatchReturnsNew(t *testing.T) {
	old := []float32{1, 0, 0}
	newEmb := []float32{0, 1}
	got := BlendCentroid(old, newEmb, 0.3)
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("got %v, want new embedding on length mismatch", got)
	}
}

func TestBlendCentroid_WeightsAndNormalizes(t *testing.T) {
	old := []float32{1, 0, 0, 0}
	newEmb := []float32{0, 1, 0, 0}
	got := BlendCentroid(old, newEmb, 0.3)
	// merged = [0.7, 0.3, 0, 0]; old component must dominate after weighting.
	if got[0] <= got[1] {
		t.Errorf("got %v, want first component (alpha=0.3 favors old) larger", got)
	}
	if n := l2Norm(got); math.Abs(n-1.0) > 1e-4 {
		t.Errorf("result L2 norm = %v, want 1.0", n)
	}
}

func TestAverageNormalized_MidpointIsUnitLength(t *testing.T) {
	got := AverageNormalized([]float32{1, 0}, []float32{0, 1})
	if math.Abs(float64(got[0])-float64(got[1])) > 1e-5 {
		t.Errorf("got %v, want symmetric components", got)
	}
	if n := l2Norm(got); math.Abs(n-1.0) > 1e-4 {
		t.Errorf("result L2 norm = %v, want 1.0", n)
	}
}

func TestAverageNormalized_LengthMismatchReturnsA(t *testing.T) {
	a := []float32{1, 2, 3}
	got := AverageNormalized(a, []float32{1, 2})
	if len(got) != 3 || got[0] != 1 {
		t.Errorf("got %v, want a unchanged on length mismatch", got)
	}
}
