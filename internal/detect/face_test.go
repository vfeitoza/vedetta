package detect

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestL2Normalize(t *testing.T) {
	v := []float32{3, 4}
	l2Normalize(v)

	// Expected: [0.6, 0.8]
	if math.Abs(float64(v[0])-0.6) > 1e-5 || math.Abs(float64(v[1])-0.8) > 1e-5 {
		t.Errorf("l2Normalize([3,4]) = %v, want [0.6, 0.8]", v)
	}

	// Verify unit norm
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if math.Abs(norm-1.0) > 1e-5 {
		t.Errorf("normalized vector norm = %f, want 1.0", norm)
	}
}

func TestL2Normalize_ZeroVector(t *testing.T) {
	v := []float32{0, 0, 0}
	l2Normalize(v)

	// Should not panic or produce NaN
	for i, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			t.Errorf("l2Normalize zero vector: element %d = %f", i, x)
		}
	}
}

func TestL2Normalize_HighDim(t *testing.T) {
	// Simulate a 512-dim embedding
	v := make([]float32, 512)
	for i := range v {
		v[i] = float32(i) * 0.01
	}
	l2Normalize(v)

	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if math.Abs(norm-1.0) > 1e-4 {
		t.Errorf("512-dim normalized vector norm = %f, want 1.0", norm)
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors should have similarity 1.0
	a := []float32{0.6, 0.8}
	sim := CosineSimilarity(a, a)
	if math.Abs(sim-1.0) > 1e-5 {
		t.Errorf("cosine(a, a) = %f, want 1.0", sim)
	}

	// Orthogonal vectors should have similarity 0.0
	b := []float32{-0.8, 0.6}
	sim = CosineSimilarity(a, b)
	if math.Abs(sim) > 1e-5 {
		t.Errorf("cosine(orthogonal) = %f, want 0.0", sim)
	}

	// Opposite vectors should have similarity -1.0
	c := []float32{-0.6, -0.8}
	sim = CosineSimilarity(a, c)
	if math.Abs(sim+1.0) > 1e-5 {
		t.Errorf("cosine(opposite) = %f, want -1.0", sim)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0, 0}
	sim := CosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("cosine(different lengths) = %f, want 0", sim)
	}
}

func TestCropRegion(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			frame.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0, A: 255})
		}
	}

	// Normal crop within bounds
	crop := cropRegion(frame, [4]int{10, 20, 50, 60})
	bounds := crop.Bounds()
	if bounds.Dx() != 40 || bounds.Dy() != 40 {
		t.Errorf("crop size = %dx%d, want 40x40", bounds.Dx(), bounds.Dy())
	}

	// Verify pixel values are from the correct region
	r, g, _, _ := crop.At(10, 20).RGBA()
	if r>>8 != 10 || g>>8 != 20 {
		t.Errorf("crop pixel (10,20) = (%d, %d), want (10, 20)", r>>8, g>>8)
	}
}

func TestCropRegion_ClampedToBounds(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))

	// Box extends beyond frame
	crop := cropRegion(frame, [4]int{-10, -10, 110, 110})
	bounds := crop.Bounds()
	if bounds.Dx() != 100 || bounds.Dy() != 100 {
		t.Errorf("clamped crop size = %dx%d, want 100x100", bounds.Dx(), bounds.Dy())
	}
}

func TestCropRegion_EmptyBox(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))

	// Inverted box
	crop := cropRegion(frame, [4]int{50, 50, 10, 10})
	bounds := crop.Bounds()
	if bounds.Dx() != 0 || bounds.Dy() != 0 {
		t.Errorf("inverted box crop size = %dx%d, want 0x0", bounds.Dx(), bounds.Dy())
	}
}

func TestNmsFaces(t *testing.T) {
	faces := []scrfdFace{
		{box: [4]int{0, 0, 100, 100}, score: 0.9},
		{box: [4]int{5, 5, 105, 105}, score: 0.8}, // high overlap with first
		{box: [4]int{200, 200, 300, 300}, score: 0.7}, // no overlap
	}

	result := nmsFaces(faces, 0.4)
	if len(result) != 2 {
		t.Fatalf("nmsFaces returned %d faces, want 2", len(result))
	}

	// Should keep highest score and non-overlapping
	if result[0].score != 0.9 {
		t.Errorf("first face score = %f, want 0.9", result[0].score)
	}
	if result[1].score != 0.7 {
		t.Errorf("second face score = %f, want 0.7", result[1].score)
	}
}

func TestNmsFaces_Empty(t *testing.T) {
	result := nmsFaces(nil, 0.4)
	if result != nil {
		t.Errorf("nmsFaces(nil) = %v, want nil", result)
	}
}

func TestNmsFaces_Single(t *testing.T) {
	faces := []scrfdFace{
		{box: [4]int{0, 0, 100, 100}, score: 0.9},
	}
	result := nmsFaces(faces, 0.4)
	if len(result) != 1 {
		t.Fatalf("nmsFaces(single) returned %d, want 1", len(result))
	}
}

func TestPrepareFaceNetInput_Range(t *testing.T) {
	// Create a FaceRecognizer with just the embedding buffer
	fr := &FaceRecognizer{}

	// Create a 112x112 test image with known pixel values
	img := image.NewRGBA(image.Rect(0, 0, 112, 112))
	for y := range 112 {
		for x := range 112 {
			img.SetRGBA(x, y, color.RGBA{R: 0, G: 127, B: 255, A: 255})
		}
	}

	buf := fr.prepareFaceNetInput(img)

	if len(buf) != 3*112*112 {
		t.Fatalf("buffer size = %d, want %d", len(buf), 3*112*112)
	}

	channelStride := 112 * 112

	// R channel: (0 - 127.5) / 127.5 ≈ -1.0
	rVal := buf[0]
	if math.Abs(float64(rVal)+1.0) > 0.01 {
		t.Errorf("R channel value = %f, want ≈ -1.0", rVal)
	}

	// G channel: (127 - 127.5) / 127.5 ≈ -0.004
	gVal := buf[channelStride]
	if math.Abs(float64(gVal)) > 0.01 {
		t.Errorf("G channel value = %f, want ≈ 0.0", gVal)
	}

	// B channel: (255 - 127.5) / 127.5 ≈ 1.0
	bVal := buf[2*channelStride]
	if math.Abs(float64(bVal)-1.0) > 0.01 {
		t.Errorf("B channel value = %f, want ≈ 1.0", bVal)
	}
}

func TestPrepareSCRFDInput_Dimensions(t *testing.T) {
	fr := &FaceRecognizer{}

	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	buf, scale, padX, padY := fr.prepareSCRFDInput(img)

	if len(buf) != 3*640*640 {
		t.Fatalf("buffer size = %d, want %d", len(buf), 3*640*640)
	}

	// Scale should be 640/320 = 2.0 (width is the limiting factor)
	expectedScale := 640.0 / 320.0
	if math.Abs(scale-expectedScale) > 1e-6 {
		t.Errorf("scale = %f, want %f", scale, expectedScale)
	}

	// newW = 320*2 = 640, newH = 240*2 = 480
	// padX = (640-640)/2 = 0, padY = (640-480)/2 = 80
	if math.Abs(padX) > 1e-6 {
		t.Errorf("padX = %f, want 0", padX)
	}
	if math.Abs(padY-80) > 1e-6 {
		t.Errorf("padY = %f, want 80", padY)
	}
}

// prepareSCRFDInput is called on the result of cropRegion, which is an
// image.RGBA SubImage: its Pix slice is re-sliced to start at the crop's
// top-left and its Rect.Min is the non-zero crop origin. Indexing Pix with a
// full-frame-relative offset (adding bounds.Min again on top of the already
// offset slice) double-counts the origin and reads far past the shortened
// sub-slice. For any non-origin crop this panics with index out of range,
// which makes face recognition fail on every detected person whose box does
// not start at (0,0) - i.e. essentially always.
func TestPrepareSCRFDInput_OffsetSubImageCrop(t *testing.T) {
	fr := &FaceRecognizer{}

	// A full camera-sized frame; the person crop sits in the bottom-right so
	// the double-counted origin offset overshoots the re-sliced Pix slice.
	frame := image.NewRGBA(image.Rect(0, 0, 640, 640))
	cropRect := image.Rect(320, 320, 640, 640)
	for y := cropRect.Min.Y; y < cropRect.Max.Y; y++ {
		for x := cropRect.Min.X; x < cropRect.Max.X; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	crop := cropRegion(frame, [4]int{cropRect.Min.X, cropRect.Min.Y, cropRect.Max.X, cropRect.Max.Y})

	buf, _, _, _ := fr.prepareSCRFDInput(crop)

	// The crop is solid red, so the first sampled cell (channel 0) must carry
	// the normalized red value, proving the right pixels were read - not a
	// pre-fill artifact and not garbage from a wild offset.
	channelStride := scrfdInputSize * scrfdInputSize
	wantR := float32((255.0 - 127.5) / 128.0)
	if math.Abs(float64(buf[0]-wantR)) > 0.01 {
		t.Errorf("sampled R at crop origin = %f, want ≈ %f (crop is solid red)", buf[0], wantR)
	}
	// Green channel of a pure-red pixel normalizes to ≈ -0.996.
	wantG := float32((0.0 - 127.5) / 128.0)
	if math.Abs(float64(buf[channelStride]-wantG)) > 0.01 {
		t.Errorf("sampled G at crop origin = %f, want ≈ %f", buf[channelStride], wantG)
	}
}

func TestPrepareSCRFDInput_Normalization(t *testing.T) {
	fr := &FaceRecognizer{}

	// Create image with pixel value 127 (should normalize to ≈ 0)
	img := image.NewRGBA(image.Rect(0, 0, 640, 640))
	for y := range 640 {
		for x := range 640 {
			img.SetRGBA(x, y, color.RGBA{R: 127, G: 127, B: 127, A: 255})
		}
	}

	buf, _, _, _ := fr.prepareSCRFDInput(img)

	// Center pixel should be ≈ (127 - 127.5) / 128.0 ≈ -0.0039
	val := buf[320*640+320]
	if math.Abs(float64(val)) > 0.01 {
		t.Errorf("normalized 127 pixel = %f, want ≈ 0", val)
	}
}
