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
