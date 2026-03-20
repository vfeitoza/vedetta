package detect

import (
	"image"
	"testing"
)

func TestPrepareInput_OutputShape(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	input, scale, padX, padY := prepareInput(img)

	// Should produce a flat CHW tensor of 3*640*640
	expectedLen := 3 * modelInputSize * modelInputSize
	if len(input) != expectedLen {
		t.Errorf("expected tensor length %d, got %d", expectedLen, len(input))
	}

	if scale <= 0 {
		t.Errorf("expected positive scale, got %f", scale)
	}

	// 1920x1080 → scale = 640/1920 = 0.333, newW=640, newH=360, padX=0, padY=140
	if padX < 0 || padY < 0 {
		t.Errorf("expected non-negative padding, got padX=%f padY=%f", padX, padY)
	}
}

func TestPrepareInput_SquareImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 640))
	_, scale, padX, padY := prepareInput(img)

	if scale != 1.0 {
		t.Errorf("expected scale 1.0 for 640x640 input, got %f", scale)
	}
	if padX != 0 || padY != 0 {
		t.Errorf("expected zero padding for 640x640 input, got padX=%f padY=%f", padX, padY)
	}
}

func TestNMS_RemovesDuplicates(t *testing.T) {
	detections := []Detection{
		{Label: "person", Score: 0.9, Box: [4]int{10, 10, 100, 100}},
		{Label: "person", Score: 0.8, Box: [4]int{12, 12, 102, 102}}, // overlaps heavily
		{Label: "person", Score: 0.7, Box: [4]int{200, 200, 300, 300}}, // separate
	}

	result := nms(detections, 0.5)

	if len(result) != 2 {
		t.Errorf("expected 2 detections after NMS, got %d", len(result))
	}

	// Should keep the highest score from the overlapping pair
	if result[0].Score != 0.9 {
		t.Errorf("expected top detection score 0.9, got %f", result[0].Score)
	}
}

func TestNMS_DifferentClasses(t *testing.T) {
	detections := []Detection{
		{Label: "person", Score: 0.9, Box: [4]int{10, 10, 100, 100}},
		{Label: "car", Score: 0.8, Box: [4]int{10, 10, 100, 100}}, // same box, different class
	}

	result := nms(detections, 0.5)

	// NMS only suppresses within the same class
	if len(result) != 2 {
		t.Errorf("expected 2 detections (different classes), got %d", len(result))
	}
}

func TestPrepareInputFromRGB24_MatchesRGBA(t *testing.T) {
	w, h := 320, 240
	// Build matching RGB24 and RGBA images with identical pixel data.
	rgb24 := make([]byte, w*h*3)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range w * h {
		r, g, b := byte(i%256), byte((i*7)%256), byte((i*13)%256)
		rgb24[i*3+0] = r
		rgb24[i*3+1] = g
		rgb24[i*3+2] = b
		img.Pix[i*4+0] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = 255
	}

	bufRGBA := make([]float32, 3*modelInputSize*modelInputSize)
	bufRGB24 := make([]float32, 3*modelInputSize*modelInputSize)

	outRGBA, scaleA, padXA, padYA := prepareInputInto(bufRGBA, img)
	outRGB24, scaleB, padXB, padYB := prepareInputFromRGB24Into(bufRGB24, rgb24, w, h)

	if scaleA != scaleB || padXA != padXB || padYA != padYB {
		t.Fatalf("metadata mismatch: RGBA(%f,%f,%f) vs RGB24(%f,%f,%f)",
			scaleA, padXA, padYA, scaleB, padXB, padYB)
	}

	for i := range outRGBA {
		if outRGBA[i] != outRGB24[i] {
			t.Fatalf("tensor mismatch at index %d: RGBA=%f, RGB24=%f", i, outRGBA[i], outRGB24[i])
		}
	}
}

func TestPrepareInputFromRGB24_OutputShape(t *testing.T) {
	w, h := 1920, 1080
	data := make([]byte, w*h*3)
	buf := make([]float32, 3*modelInputSize*modelInputSize)
	out, scale, padX, padY := prepareInputFromRGB24Into(buf, data, w, h)

	expectedLen := 3 * modelInputSize * modelInputSize
	if len(out) != expectedLen {
		t.Errorf("expected tensor length %d, got %d", expectedLen, len(out))
	}
	if scale <= 0 {
		t.Errorf("expected positive scale, got %f", scale)
	}
	if padX < 0 || padY < 0 {
		t.Errorf("expected non-negative padding, got padX=%f padY=%f", padX, padY)
	}
}

func TestIOU_FullOverlap(t *testing.T) {
	a := [4]int{0, 0, 100, 100}
	result := iou(a, a)
	if result != 1.0 {
		t.Errorf("expected IoU 1.0 for identical boxes, got %f", result)
	}
}

func TestIOU_NoOverlap(t *testing.T) {
	a := [4]int{0, 0, 50, 50}
	b := [4]int{100, 100, 200, 200}
	result := iou(a, b)
	if result != 0 {
		t.Errorf("expected IoU 0 for non-overlapping boxes, got %f", result)
	}
}
