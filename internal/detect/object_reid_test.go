package detect

import (
	"image"
	"math"
	"testing"
)

func TestObjectEmbedder_Embed(t *testing.T) {
	oe, err := NewObjectEmbedder(ObjectEmbedderConfig{})
	if err != nil {
		t.Skipf("OSNet model not available: %v", err)
	}

	// Create a synthetic 200x300 RGBA image
	img := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 256)
	}

	emb, err := oe.Embed(img, [4]int{10, 10, 190, 290})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(emb) == 0 {
		t.Fatal("expected non-empty embedding")
	}

	// Check L2-normalized
	var norm float64
	for _, v := range emb {
		norm += float64(v) * float64(v)
	}
	if math.Abs(norm-1.0) > 0.01 {
		t.Errorf("embedding norm = %f, want ~1.0", norm)
	}

	t.Logf("embedding dim=%d, norm=%.4f", len(emb), norm)
}
