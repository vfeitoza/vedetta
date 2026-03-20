package detect

import (
	"fmt"
	"image"
	"log/slog"
	"os"
	"sync"

	"github.com/rvben/watchpost/internal/config"
)

// Detection represents a single detected object.
type Detection struct {
	Label string
	Score float32
	Box   [4]int // x1, y1, x2, y2
}

// Detector runs object detection on image frames.
// It selects the best available backend automatically.
// Safe for concurrent use by multiple camera goroutines.
type Detector struct {
	mu      sync.Mutex
	config  config.DetectConfig
	backend Backend
	enabled bool
}

func New(cfg config.DetectConfig) *Detector {
	d := &Detector{
		config: cfg,
	}

	if err := d.init(cfg); err != nil {
		slog.Warn("object detection unavailable, using motion-only mode",
			"reason", err.Error(),
		)
		return d
	}

	d.enabled = true
	slog.Info("object detection initialized", "backend", d.backend.Name())

	return d
}

func (d *Detector) MotionThreshold() float64 {
	return d.config.MotionThreshold
}

// Detect runs object detection on a frame and returns detections above threshold.
// Safe for concurrent use — serializes access to the backend.
func (d *Detector) Detect(img *image.RGBA) []Detection {
	if !d.enabled {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	inputData, scale, padX, padY := prepareInput(img)

	output, err := d.backend.Run(inputData)
	if err != nil {
		slog.Error("inference failed", "error", err)
		return nil
	}

	return processOutput(output, d.config.ScoreThreshold, scale, padX, padY)
}

func (d *Detector) Close() {
	if d.backend != nil {
		d.backend.Close()
	}
}

func (d *Detector) init(cfg config.DetectConfig) error {
	modelData, err := d.loadModelData(cfg.ModelPath)
	if err != nil {
		return fmt.Errorf("load model: %w", err)
	}

	backend, err := selectBackend(cfg.Backend, modelData)
	if err != nil {
		return err
	}

	d.backend = backend
	return nil
}

// selectBackend picks the best available backend based on config and build tags.
func selectBackend(preference string, modelData []byte) (Backend, error) {
	switch preference {
	case "go":
		return NewGoBackend(modelData)

	case "onnxruntime_c":
		b, err := NewCAPIBackend(modelData)
		if err != nil {
			return nil, fmt.Errorf("C ONNX Runtime backend: %w", err)
		}
		return b, nil

	case "", "auto":
		// Try C ONNX Runtime first (faster), fall back to pure Go.
		b, err := NewCAPIBackend(modelData)
		if err == nil {
			slog.Info("auto-selected C ONNX Runtime backend")
			return b, nil
		}
		slog.Info("C ONNX Runtime not available, using pure Go backend", "reason", err.Error())
		return NewGoBackend(modelData)

	default:
		return nil, fmt.Errorf("unknown backend %q: use \"auto\", \"go\", or \"onnxruntime_c\"", preference)
	}
}

// loadModelData resolves model bytes from config path, embedded data, or common locations.
func (d *Detector) loadModelData(modelPath string) ([]byte, error) {
	if modelPath != "" {
		slog.Info("loading model from path", "path", modelPath)
		data, err := os.ReadFile(modelPath)
		if err != nil {
			return nil, fmt.Errorf("read model file %q: %w", modelPath, err)
		}
		return data, nil
	}

	if len(embeddedModel) > 0 {
		slog.Info("using embedded model")
		return embeddedModel, nil
	}

	candidates := []string{
		"yolov8n.onnx",
		"/tmp/yolov8n.onnx",
	}
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			slog.Info("found model at", "path", path)
			return data, nil
		}
	}

	return nil, fmt.Errorf("no model found: set detect.model_path in config, embed with build tag, or place yolov8n.onnx in working directory")
}
