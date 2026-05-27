package snapshot

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/rvben/vedetta/internal/detect"
)

func testFrame(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill with a mid-gray background
	for i := range img.Pix {
		if i%4 == 3 {
			img.Pix[i] = 255 // alpha
		} else {
			img.Pix[i] = 128 // gray
		}
	}
	return img
}

func TestDrawDetections(t *testing.T) {
	img := testFrame(640, 480)
	detections := []detect.Detection{
		{Label: "person", Score: 0.95, Box: [4]int{50, 100, 200, 400}},
		{Label: "car", Score: 0.80, Box: [4]int{300, 200, 550, 450}},
	}

	result := DrawDetections(img, detections)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	bounds := result.Bounds()
	if bounds.Dx() != 640 || bounds.Dy() != 480 {
		t.Errorf("expected 640x480, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	// Verify the bounding box pixels were modified (person box at x=50, y=100 should be red)
	r, _, _, _ := result.At(50, 100).RGBA()
	if r>>8 != 255 {
		t.Errorf("expected red pixel at person box corner, got R=%d", r>>8)
	}

	// Verify car box (blue at x=300, y=200)
	_, _, b, _ := result.At(300, 200).RGBA()
	if b>>8 != 255 {
		t.Errorf("expected blue pixel at car box corner, got B=%d", b>>8)
	}
}

func TestDrawDetectionsEmpty(t *testing.T) {
	img := testFrame(320, 240)
	result := DrawDetections(img, nil)
	if result == nil {
		t.Fatal("expected non-nil result even with no detections")
	}
}

func TestDrawDetectionsUnknownLabel(t *testing.T) {
	img := testFrame(640, 480)
	detections := []detect.Detection{
		{Label: "unknown_object", Score: 0.7, Box: [4]int{10, 10, 100, 100}},
	}

	result := DrawDetections(img, detections)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Unknown label should use default cyan color
	r, g, b, _ := result.At(10, 10).RGBA()
	if r>>8 != 0 || g>>8 != 255 || b>>8 != 255 {
		t.Errorf("expected cyan for unknown label, got R=%d G=%d B=%d", r>>8, g>>8, b>>8)
	}
}

func TestDrawDetectionsClampsToBounds(t *testing.T) {
	img := testFrame(100, 100)
	detections := []detect.Detection{
		// Box extends beyond image bounds
		{Label: "person", Score: 0.9, Box: [4]int{-10, -10, 200, 200}},
	}

	// Should not panic
	result := DrawDetections(img, detections)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSaveSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test_snapshot.jpg")

	img := testFrame(320, 240)

	err := SaveSnapshot(img, path, 85)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file exists and is valid JPEG
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open snapshot: %v", err)
	}
	defer func() { _ = f.Close() }()

	decoded, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("saved file is not valid JPEG: %v", err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != 320 || bounds.Dy() != 240 {
		t.Errorf("expected 320x240, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestSaveSnapshotCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nested", "dir", "snap.jpg")

	img := testFrame(100, 100)

	err := SaveSnapshot(img, path, 85)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected file to exist")
	}
}

func TestSaveSnapshotDefaultQuality(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "default_quality.jpg")

	img := testFrame(100, 100)

	// Quality 0 should default to 85
	err := SaveSnapshot(img, path, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("expected non-zero file size")
	}
}

func TestColorForLabel(t *testing.T) {
	tests := []struct {
		label   string
		r, g, b uint8
	}{
		{"person", 255, 0, 0},
		{"car", 0, 0, 255},
		{"cat", 0, 255, 0},
		{"unknown", 0, 255, 255}, // default cyan
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			c := colorForLabel(tt.label)
			if c.R != tt.r || c.G != tt.g || c.B != tt.b {
				t.Errorf("colorForLabel(%q) = (%d,%d,%d), want (%d,%d,%d)",
					tt.label, c.R, c.G, c.B, tt.r, tt.g, tt.b)
			}
		})
	}
}
