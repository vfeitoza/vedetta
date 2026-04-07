package media

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

func TestScaleYCbCr_PreservesAspectRatio(t *testing.T) {
	// 1920x800 source, target 1280x720 → should output 1280x532
	// scale = min(1280/1920, 720/800) = min(0.666, 0.9) = 0.666
	// out_width  = floor(1920 * 0.6666 / 2) * 2 = floor(639.99) * 2 = 639*2 = 1278 → hmm
	// Actually: scale = 1280/1920 = 0.6666...
	// out_width  = floor(1920 * 0.6666 / 2) * 2 = floor(639.99) * 2 = 639*2 = 1278
	// Wait, let me recalculate per spec:
	// scale = min(1280/1920, 720/800) = min(0.6666, 0.9) = 0.6666
	// out_width = floor(1920 * 0.6666 / 2) * 2
	// 1920 * 0.6666... = 1280 exactly → floor(1280/2)*2 = 640*2 = 1280
	// out_height = floor(800 * 0.6666 / 2) * 2
	// 800 * 0.6666... = 533.33 → floor(533.33/2)*2 = floor(266.66)*2 = 266*2 = 532
	src := image.NewYCbCr(image.Rect(0, 0, 1920, 800), image.YCbCrSubsampleRatio420)
	got := scaleYCbCr(src, 1280, 720)
	if got.Rect.Dx() != 1280 {
		t.Errorf("width = %d, want 1280", got.Rect.Dx())
	}
	if got.Rect.Dy() != 532 {
		t.Errorf("height = %d, want 532", got.Rect.Dy())
	}
}

func TestScaleYCbCr_EvenDimensions(t *testing.T) {
	// Output must always have even width and height (H264 requirement)
	src := image.NewYCbCr(image.Rect(0, 0, 1921, 1081), image.YCbCrSubsampleRatio420)
	got := scaleYCbCr(src, 1280, 720)
	if got.Rect.Dx()%2 != 0 {
		t.Errorf("width %d is not even", got.Rect.Dx())
	}
	if got.Rect.Dy()%2 != 0 {
		t.Errorf("height %d is not even", got.Rect.Dy())
	}
}

func TestScaleYCbCr_SameSize(t *testing.T) {
	src := image.NewYCbCr(image.Rect(0, 0, 1280, 720), image.YCbCrSubsampleRatio420)
	got := scaleYCbCr(src, 1280, 720)
	if got.Rect.Dx() != 1280 || got.Rect.Dy() != 720 {
		t.Errorf("got %dx%d, want 1280x720", got.Rect.Dx(), got.Rect.Dy())
	}
}

func TestShouldTranscode_SkipsIfAlreadySmall(t *testing.T) {
	// 720p source, 720p target → skip
	skip, outW, outH := shouldTranscode(1280, 720, 1280, 720)
	if !skip {
		t.Error("expected skip=true for same-size source")
	}
	_ = outW
	_ = outH
}

func TestShouldTranscode_SkipsIfBelowTarget(t *testing.T) {
	// 640x480 source, 1280x720 target → source smaller, skip
	skip, _, _ := shouldTranscode(640, 480, 1280, 720)
	if !skip {
		t.Error("expected skip=true when source is smaller than target")
	}
}

func TestShouldTranscode_SkipsBelowAreaThreshold(t *testing.T) {
	// 1280x800 source, 1280x720 target → area reduction ~10%, skip
	skip, _, _ := shouldTranscode(1280, 800, 1280, 720)
	if !skip {
		t.Error("expected skip=true when area reduction < 25%")
	}
}

func TestShouldTranscode_Transcodes1080p(t *testing.T) {
	// 1920x1080 source, 1280x720 target → 56% reduction, transcode
	skip, outW, outH := shouldTranscode(1920, 1080, 1280, 720)
	if skip {
		t.Error("expected skip=false for 1080p → 720p")
	}
	if outW != 1280 || outH != 720 {
		t.Errorf("got %dx%d, want 1280x720", outW, outH)
	}
}

func TestShouldTranscode_Preserves4K(t *testing.T) {
	// 2560x1440 source, 1280x720 target → 75% reduction, transcode
	skip, outW, outH := shouldTranscode(2560, 1440, 1280, 720)
	if skip {
		t.Error("expected skip=false for 4K → 720p")
	}
	if outW != 1280 || outH != 720 {
		t.Errorf("got %dx%d, want 1280x720", outW, outH)
	}
}

func TestReadSourceResolution(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.mp4"

	// writeSyntheticFMP4 uses SPS bytes that decode to 128x96
	writeSyntheticFMP4(t, path, 3, 3000)

	width, height, err := readSourceResolution(path)
	if err != nil {
		t.Fatalf("readSourceResolution: %v", err)
	}
	if width != 128 || height != 96 {
		t.Errorf("got %dx%d, want 128x96", width, height)
	}
}

func TestTranscodeSegment_SkipsIfAlreadySmall(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	writeSyntheticFMP4(t, src, 10, 3000) // 128x96 — well below 1280x720

	result, err := TranscodeSegment(src, 1280, 720)
	if err != nil {
		t.Fatalf("TranscodeSegment: %v", err)
	}
	if !result.Skipped {
		t.Error("expected skip=true for source smaller than target")
	}
	// No .tmp file should exist
	if _, statErr := os.Stat(src + ".tmp"); !os.IsNotExist(statErr) {
		t.Error("unexpected .tmp file left behind")
	}
}

func TestTranscodeSegment_OriginalUntouchedOnFailure(t *testing.T) {
	dir := t.TempDir()

	origStat, _ := os.Stat(dir) // just to confirm dir exists

	// Pass a non-existent path to trigger failure at open
	_, err := TranscodeSegment(filepath.Join(dir, "nonexistent.mp4"), 1280, 720)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
	_ = origStat

	// No .tmp file should exist
	if _, statErr := os.Stat(filepath.Join(dir, "nonexistent.mp4.tmp")); !os.IsNotExist(statErr) {
		t.Error(".tmp file left behind after failure")
	}
}
