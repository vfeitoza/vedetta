package detect

import "math"

// MotionScore compares two raw RGB frames and returns a normalized score
// between 0.0 (identical) and 1.0 (completely different).
// Both frames must be the same length (width * height * 3 for RGB24).
func MotionScore(prev, curr []byte) float64 {
	if len(prev) != len(curr) || len(prev) == 0 {
		return 0
	}

	var totalDiff uint64
	pixels := len(prev) / 3

	for i := 0; i < len(prev); i += 3 {
		// Average the RGB channels for a simple luminance approximation
		prevLum := int(prev[i]) + int(prev[i+1]) + int(prev[i+2])
		currLum := int(curr[i]) + int(curr[i+1]) + int(curr[i+2])
		diff := prevLum - currLum
		if diff < 0 {
			diff = -diff
		}
		totalDiff += uint64(diff)
	}

	// Normalize: max diff per pixel is 255*3 = 765
	maxDiff := float64(pixels) * 765.0
	score := float64(totalDiff) / maxDiff

	return math.Min(score, 1.0)
}
