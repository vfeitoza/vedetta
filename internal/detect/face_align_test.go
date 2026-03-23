package detect

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestEstimateAffine_Identity(t *testing.T) {
	// When source and destination points are the same, the transform should be near-identity.
	pts := [5][2]float64{
		{10, 20}, {30, 20}, {20, 35}, {12, 45}, {28, 45},
	}
	M := estimateAffine(pts, pts)

	// a ≈ 1, b ≈ 0, tx ≈ 0, c ≈ 0, d ≈ 1, ty ≈ 0
	if math.Abs(M[0]-1) > 1e-6 || math.Abs(M[1]) > 1e-6 || math.Abs(M[2]) > 1e-6 {
		t.Errorf("expected identity-like first row, got [%f, %f, %f]", M[0], M[1], M[2])
	}
	if math.Abs(M[3]) > 1e-6 || math.Abs(M[4]-1) > 1e-6 || math.Abs(M[5]) > 1e-6 {
		t.Errorf("expected identity-like second row, got [%f, %f, %f]", M[3], M[4], M[5])
	}
}

func TestEstimateAffine_Translation(t *testing.T) {
	// Pure translation: dst = src + (10, 20)
	src := [5][2]float64{
		{10, 20}, {30, 20}, {20, 35}, {12, 45}, {28, 45},
	}
	var dst [5][2]float64
	for i := range src {
		dst[i][0] = src[i][0] + 10
		dst[i][1] = src[i][1] + 20
	}

	M := estimateAffine(src, dst)

	if math.Abs(M[0]-1) > 1e-6 || math.Abs(M[1]) > 1e-6 || math.Abs(M[2]-10) > 1e-6 {
		t.Errorf("expected [1, 0, 10], got [%f, %f, %f]", M[0], M[1], M[2])
	}
	if math.Abs(M[3]) > 1e-6 || math.Abs(M[4]-1) > 1e-6 || math.Abs(M[5]-20) > 1e-6 {
		t.Errorf("expected [0, 1, 20], got [%f, %f, %f]", M[3], M[4], M[5])
	}
}

func TestEstimateAffine_Scale(t *testing.T) {
	// Scale by 2x
	src := [5][2]float64{
		{10, 20}, {30, 20}, {20, 35}, {12, 45}, {28, 45},
	}
	var dst [5][2]float64
	for i := range src {
		dst[i][0] = src[i][0] * 2
		dst[i][1] = src[i][1] * 2
	}

	M := estimateAffine(src, dst)

	if math.Abs(M[0]-2) > 1e-6 || math.Abs(M[1]) > 1e-6 || math.Abs(M[2]) > 1e-6 {
		t.Errorf("expected [2, 0, 0], got [%f, %f, %f]", M[0], M[1], M[2])
	}
	if math.Abs(M[3]) > 1e-6 || math.Abs(M[4]-2) > 1e-6 || math.Abs(M[5]) > 1e-6 {
		t.Errorf("expected [0, 2, 0], got [%f, %f, %f]", M[3], M[4], M[5])
	}
}

func TestEstimateAffine_PointMapping(t *testing.T) {
	// Verify that estimated transform maps source points to destination points
	src := [5][2]float64{
		{50, 60}, {90, 58}, {70, 80}, {55, 100}, {85, 98},
	}
	dst := canonicalLandmarks

	M := estimateAffine(src, dst)

	for i := range src {
		mx := M[0]*src[i][0] + M[1]*src[i][1] + M[2]
		my := M[3]*src[i][0] + M[4]*src[i][1] + M[5]

		dx := math.Abs(mx - dst[i][0])
		dy := math.Abs(my - dst[i][1])

		if dx > 1.0 || dy > 1.0 {
			t.Errorf("point %d: mapped to (%.2f, %.2f), want (%.2f, %.2f)",
				i, mx, my, dst[i][0], dst[i][1])
		}
	}
}

func TestAlignFace_OutputSize(t *testing.T) {
	// Create a test image
	src := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}

	landmarks := [5][2]float32{
		{50, 60}, {90, 58}, {70, 80}, {55, 100}, {85, 98},
	}

	result := alignFace(src, landmarks)

	bounds := result.Bounds()
	if bounds.Dx() != 112 || bounds.Dy() != 112 {
		t.Errorf("expected 112x112, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestAlignFace_NotAllBlack(t *testing.T) {
	// Verify the warp produces non-zero pixels when landmarks are within the image
	src := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			src.SetRGBA(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}

	landmarks := [5][2]float32{
		{50, 60}, {90, 58}, {70, 80}, {55, 100}, {85, 98},
	}

	result := alignFace(src, landmarks)

	nonZero := 0
	for i := 0; i < len(result.Pix); i += 4 {
		if result.Pix[i] > 0 || result.Pix[i+1] > 0 || result.Pix[i+2] > 0 {
			nonZero++
		}
	}

	if nonZero == 0 {
		t.Error("aligned face is completely black — warp produced no valid pixels")
	}

	// Most pixels should be non-zero for a well-centered face
	totalPixels := 112 * 112
	if float64(nonZero)/float64(totalPixels) < 0.5 {
		t.Errorf("only %d/%d pixels are non-zero — warp may be incorrect", nonZero, totalPixels)
	}
}

func TestBilinear(t *testing.T) {
	// At corners, bilinear should return the corner value
	if v := bilinear(10, 20, 30, 40, 0, 0); math.Abs(v-10) > 1e-6 {
		t.Errorf("bilinear(0,0) = %f, want 10", v)
	}
	if v := bilinear(10, 20, 30, 40, 1, 0); math.Abs(v-20) > 1e-6 {
		t.Errorf("bilinear(1,0) = %f, want 20", v)
	}
	if v := bilinear(10, 20, 30, 40, 0, 1); math.Abs(v-30) > 1e-6 {
		t.Errorf("bilinear(0,1) = %f, want 30", v)
	}
	if v := bilinear(10, 20, 30, 40, 1, 1); math.Abs(v-40) > 1e-6 {
		t.Errorf("bilinear(1,1) = %f, want 40", v)
	}
	// Center should be average
	if v := bilinear(10, 20, 30, 40, 0.5, 0.5); math.Abs(v-25) > 1e-6 {
		t.Errorf("bilinear(0.5,0.5) = %f, want 25", v)
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		v, lo, hi, want float64
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
	}
	for _, tc := range tests {
		got := clamp(tc.v, tc.lo, tc.hi)
		if got != tc.want {
			t.Errorf("clamp(%f, %f, %f) = %f, want %f", tc.v, tc.lo, tc.hi, got, tc.want)
		}
	}
}
