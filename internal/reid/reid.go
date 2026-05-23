// Package reid holds the pure embedding-matching and centroid-maintenance
// policy used by face recognition and object re-identification. It contains no
// I/O: callers fetch candidates and persist results, while the similarity
// decisions and centroid math live here so they can be tested in isolation.
package reid

import (
	"math"

	"github.com/rvben/vedetta/internal/detect"
)

// Candidate is a stored centroid an embedding can be matched against.
type Candidate struct {
	ID       int64
	Centroid []float32
	Ignore   bool
}

// BestMatch returns the ID and cosine similarity of the candidate most similar
// to embedding, but only when that similarity meets threshold. Candidates that
// are ignored or have an empty centroid are skipped. When nothing qualifies it
// returns (0, 0).
func BestMatch(embedding []float32, candidates []Candidate, threshold float64) (int64, float64) {
	var bestID int64
	var bestSim float64
	for _, c := range candidates {
		if c.Ignore || len(c.Centroid) == 0 {
			continue
		}
		sim := detect.CosineSimilarity(embedding, c.Centroid)
		if sim > bestSim {
			bestSim = sim
			bestID = c.ID
		}
	}
	if bestSim >= threshold {
		return bestID, bestSim
	}
	return 0, 0
}

// BlendCentroid merges newEmbedding into old using an exponential running
// average (weight alpha on the new sample) and L2-normalizes the result. When
// old is empty or a different length than newEmbedding, newEmbedding is
// returned unchanged so the caller stores the fresh sample directly.
func BlendCentroid(old, newEmbedding []float32, alpha float32) []float32 {
	if len(old) == 0 || len(old) != len(newEmbedding) {
		return newEmbedding
	}
	merged := make([]float32, len(old))
	var norm float64
	for i := range merged {
		merged[i] = (1-alpha)*old[i] + alpha*newEmbedding[i]
		norm += float64(merged[i]) * float64(merged[i])
	}
	return normalize(merged, norm)
}

// AverageNormalized returns the L2-normalized midpoint of a and b. When the
// lengths differ it returns a unchanged.
func AverageNormalized(a, b []float32) []float32 {
	if len(a) != len(b) {
		return a
	}
	out := make([]float32, len(a))
	var norm float64
	for i := range out {
		out[i] = (a[i] + b[i]) / 2
		norm += float64(out[i]) * float64(out[i])
	}
	return normalize(out, norm)
}

// normalize scales v to unit length in place when its squared norm is non-zero,
// then returns it. A near-zero norm is left unscaled to avoid dividing by zero.
func normalize(v []float32, norm float64) []float32 {
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range v {
			v[i] *= invNorm
		}
	}
	return v
}
