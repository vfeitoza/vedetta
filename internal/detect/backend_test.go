package detect

import (
	"fmt"
	"image"
	"os"
	"sync"
	"testing"

	"github.com/rvben/vedetta/internal/config"
)

// --- mock backend for unit tests ---

type mockBackend struct {
	name     string
	output   []float32
	runCount int
	closed   int
	runErr   error
}

func (m *mockBackend) Run(input []float32) ([]float32, error) {
	m.runCount++
	if m.runErr != nil {
		return nil, m.runErr
	}
	return m.output, nil
}

func (m *mockBackend) Close() { m.closed++ }

func (m *mockBackend) Name() string { return m.name }

// --- interface compliance ---

func TestBackendInterfaceCompliance(t *testing.T) {
	var _ Backend = &mockBackend{}
	var _ Backend = &GoBackend{}
	var _ Backend = &CAPIBackend{}
}

// --- backend selection ---

func TestSelectBackend_Go(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := selectBackend("go", modelData)
	if err != nil {
		t.Fatalf("selectBackend(go): %v", err)
	}
	defer b.Close()

	if _, ok := b.(*GoBackend); !ok {
		t.Fatalf("expected *GoBackend, got %T", b)
	}
	if b.Name() == "" {
		t.Fatal("backend name should not be empty")
	}
}

func TestSelectBackend_AutoFallsBackToGo(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := selectBackend("auto", modelData)
	if err != nil {
		t.Fatalf("selectBackend(auto): %v", err)
	}
	defer b.Close()

	// Without cgo_onnxruntime tag, auto falls back to Go.
	if _, ok := b.(*GoBackend); !ok {
		t.Logf("auto-selected: %s (%T) — C ONNX Runtime may be available", b.Name(), b)
	}
}

func TestSelectBackend_EmptyMeansAuto(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := selectBackend("", modelData)
	if err != nil {
		t.Fatalf("selectBackend(empty): %v", err)
	}
	defer b.Close()
}

func TestSelectBackend_UnknownReturnsError(t *testing.T) {
	_, err := selectBackend("tensorrt", []byte{})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestSelectBackend_OnnxruntimeCWithoutTag(t *testing.T) {
	_, err := selectBackend("onnxruntime_c", []byte{})
	if err == nil {
		t.Fatal("expected error without cgo_onnxruntime build tag")
	}
}

// --- GoBackend ---

func TestGoBackend_Run(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}
	defer b.Close()

	input := make([]float32, inputTensorSize)
	output, err := b.Run(input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// YOLOv8n output: [1, 84, 8400] = 705600 elements.
	if len(output) != 84*8400 {
		t.Fatalf("output size = %d, want %d", len(output), 84*8400)
	}
}

func TestGoBackend_WrongInputSize(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}
	defer b.Close()

	_, err = b.Run(make([]float32, 100))
	if err == nil {
		t.Fatal("expected error for wrong input size")
	}
}

func TestGoBackend_MultipleRuns(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}
	defer b.Close()

	input := make([]float32, inputTensorSize)
	for i := range 5 {
		output, err := b.Run(input)
		if err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
		if len(output) != 84*8400 {
			t.Fatalf("Run %d: output size = %d", i, len(output))
		}
	}
}

func TestGoBackend_ConcurrentInstances(t *testing.T) {
	modelData := loadTestModel(t)

	// Each goroutine gets its own backend (single instances are not thread-safe).
	var wg sync.WaitGroup
	errs := make(chan error, 4)

	for range 4 {
		wg.Go(func() {
			b, err := NewGoBackend(modelData)
			if err != nil {
				errs <- err
				return
			}
			defer b.Close()

			input := make([]float32, inputTensorSize)
			_, err = b.Run(input)
			if err != nil {
				errs <- err
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent run failed: %v", err)
	}
}

func TestGoBackend_DoubleClose(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}
	b.Close()
	b.Close() // must not panic
}

// --- Detector end-to-end through backend ---

func TestDetector_EndToEnd_GoBackend(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}

	d := &Detector{
		backend: b,
		enabled: true,
		config: config.DetectConfig{
			ScoreThreshold: 0.5,
			Motion:         config.MotionConfig{MinRegionScore: 0.02},
		},
	}
	defer d.Close()

	// All-black 320x240 image.
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	detections := d.Detect(img)
	t.Logf("detections on black image: %d", len(detections))
}

func TestDetector_DetectRGB24(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}

	d := &Detector{
		backend: b,
		enabled: true,
		config: config.DetectConfig{
			ScoreThreshold: 0.5,
			Motion:         config.MotionConfig{MinRegionScore: 0.02},
		},
	}
	defer d.Close()

	// All-black 320x240 RGB24 frame.
	data := make([]byte, 320*240*3)
	detections := d.DetectRGB24(data, 320, 240)
	t.Logf("detections on black RGB24 frame: %d", len(detections))
}

func TestDetector_DetectRGB24_Disabled(t *testing.T) {
	d := &Detector{enabled: false}
	if detections := d.DetectRGB24(make([]byte, 100), 10, 10); detections != nil {
		t.Fatalf("expected nil from disabled detector, got %d", len(detections))
	}
}

func TestDetector_Disabled(t *testing.T) {
	d := &Detector{enabled: false}
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if detections := d.Detect(img); detections != nil {
		t.Fatalf("expected nil from disabled detector, got %d", len(detections))
	}
}

func TestDetector_BackendError(t *testing.T) {
	mock := &mockBackend{
		name:   "failing",
		runErr: fmt.Errorf("simulated failure"),
	}
	d := &Detector{
		backend: mock,
		enabled: true,
		config: config.DetectConfig{
			ScoreThreshold: 0.5,
			Motion:         config.MotionConfig{MinRegionScore: 0.02},
		},
	}
	defer d.Close()

	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	if detections := d.Detect(img); detections != nil {
		t.Fatalf("expected nil on backend error, got %d", len(detections))
	}
	if mock.runCount != 1 {
		t.Fatalf("expected 1 run, got %d", mock.runCount)
	}
}

func TestDetector_DoubleClose(t *testing.T) {
	mock := &mockBackend{name: "mock"}
	d := &Detector{backend: mock, enabled: true}
	d.Close()
	d.Close()
	if mock.closed != 2 {
		t.Fatalf("expected 2 Close calls, got %d", mock.closed)
	}
}

func TestDetector_CloseNilBackend(t *testing.T) {
	d := &Detector{}
	d.Close() // must not panic
}

func TestDetector_ConcurrentDetect(t *testing.T) {
	modelData := loadTestModel(t)
	b, err := NewGoBackend(modelData)
	if err != nil {
		t.Fatalf("NewGoBackend: %v", err)
	}

	d := &Detector{
		backend: b,
		enabled: true,
		config: config.DetectConfig{
			ScoreThreshold: 0.5,
			Motion:         config.MotionConfig{MinRegionScore: 0.02},
		},
	}
	defer d.Close()

	// Simulate 3 cameras calling Detect concurrently on a shared Detector.
	var wg sync.WaitGroup
	for cam := range 3 {
		wg.Go(func() {
			img := image.NewRGBA(image.Rect(0, 0, 320, 240))
			for frame := range 5 {
				d.Detect(img)
				_ = cam
				_ = frame
			}
		})
	}
	wg.Wait()
}

// --- CAPIBackend stub ---

func TestCAPIBackendStub_ReturnsError(t *testing.T) {
	_, err := NewCAPIBackend([]byte{})
	if err == nil {
		t.Fatal("expected error from stub CAPIBackend")
	}
}

// --- benchmarks ---

func BenchmarkGoBackend_Run(b *testing.B) {
	modelData := loadBenchModel(b)
	backend, err := NewGoBackend(modelData)
	if err != nil {
		b.Fatalf("NewGoBackend: %v", err)
	}
	defer backend.Close()

	input := make([]float32, inputTensorSize)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := backend.Run(input)
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}

func BenchmarkDetector_Detect(b *testing.B) {
	modelData := loadBenchModel(b)
	backend, err := NewGoBackend(modelData)
	if err != nil {
		b.Fatalf("NewGoBackend: %v", err)
	}

	d := &Detector{
		backend: backend,
		enabled: true,
		config: config.DetectConfig{
			ScoreThreshold: 0.5,
			Motion:         config.MotionConfig{MinRegionScore: 0.02},
		},
	}
	defer d.Close()

	img := image.NewRGBA(image.Rect(0, 0, 640, 480))

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		d.Detect(img)
	}
}

// --- helpers ---

func loadTestModel(t *testing.T) []byte {
	t.Helper()
	data, err := tryLoadModel()
	if err != nil {
		t.Skip("yolov8n.onnx model not found, skipping")
	}
	return data
}

func loadBenchModel(b *testing.B) []byte {
	b.Helper()
	data, err := tryLoadModel()
	if err != nil {
		b.Skip("yolov8n.onnx model not found, skipping")
	}
	return data
}

func tryLoadModel() ([]byte, error) {
	for _, p := range []string{
		"models/yolov8n.onnx",
		"../../internal/detect/models/yolov8n.onnx",
	} {
		if data, err := os.ReadFile(p); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("model not found")
}
