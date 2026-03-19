package detect

import (
	"fmt"
	"image"
	"log/slog"
	"os"
	"runtime"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/rvben/watchpost/internal/config"
)

// Detection represents a single detected object.
type Detection struct {
	Label string
	Score float32
	Box   [4]int // x1, y1, x2, y2
}

// Detector runs object detection on image frames using ONNX Runtime.
type Detector struct {
	config  config.DetectConfig
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	enabled bool
}

func New(cfg config.DetectConfig) *Detector {
	d := &Detector{
		config: cfg,
	}

	if cfg.ModelPath == "" {
		slog.Warn("no detection model configured, object detection disabled")
		return d
	}

	if err := d.initSession(cfg.ModelPath); err != nil {
		slog.Error("failed to initialize ONNX session, detection disabled", "error", err)
		return d
	}

	d.enabled = true
	slog.Info("object detection initialized",
		"model", cfg.ModelPath,
		"backend", detectBackend(),
	)

	return d
}

func (d *Detector) MotionThreshold() float64 {
	return d.config.MotionThreshold
}

// Detect runs object detection on a frame and returns detections above threshold.
func (d *Detector) Detect(img *image.RGBA) []Detection {
	if !d.enabled {
		return nil
	}

	// Preprocess: resize to 640x640, normalize, convert to CHW tensor
	inputData, scale, padX, padY := prepareInput(img)

	// Copy input data into the pre-allocated tensor
	copy(d.input.GetData(), inputData)

	// Run inference
	if err := d.session.Run(); err != nil {
		slog.Error("inference failed", "error", err)
		return nil
	}

	// Postprocess: extract detections, apply NMS
	return processOutput(d.output.GetData(), d.config.ScoreThreshold, scale, padX, padY)
}

func (d *Detector) Close() {
	if d.session != nil {
		_ = d.session.Destroy()
	}
	if d.input != nil {
		_ = d.input.Destroy()
	}
	if d.output != nil {
		_ = d.output.Destroy()
	}
	ort.DestroyEnvironment()
}

func (d *Detector) initSession(modelPath string) error {
	libPath := findOrtLibrary()
	if libPath == "" {
		return fmt.Errorf("could not find ONNX Runtime shared library; set ORT_LIB_PATH environment variable")
	}

	ort.SetSharedLibraryPath(libPath)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("initialize ONNX environment: %w", err)
	}

	// Create input tensor: batch=1, channels=3, height=640, width=640
	inputShape := ort.NewShape(1, 3, modelInputSize, modelInputSize)
	inputData := make([]float32, 1*3*modelInputSize*modelInputSize)
	input, err := ort.NewTensor(inputShape, inputData)
	if err != nil {
		return fmt.Errorf("create input tensor: %w", err)
	}

	// Create output tensor: batch=1, attributes=84 (4 bbox + 80 classes), detections=8400
	outputShape := ort.NewShape(1, 4+numClasses, numDetections)
	outputData := make([]float32, 1*(4+numClasses)*numDetections)
	output, err := ort.NewTensor(outputShape, outputData)
	if err != nil {
		input.Destroy()
		return fmt.Errorf("create output tensor: %w", err)
	}

	// Session options with execution provider
	options, err := ort.NewSessionOptions()
	if err != nil {
		input.Destroy()
		output.Destroy()
		return fmt.Errorf("create session options: %w", err)
	}
	defer options.Destroy()

	appendExecutionProvider(options)

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{"images"}, []string{"output0"},
		[]ort.ArbitraryTensor{input}, []ort.ArbitraryTensor{output},
		options,
	)
	if err != nil {
		input.Destroy()
		output.Destroy()
		return fmt.Errorf("create ONNX session: %w", err)
	}

	d.session = session
	d.input = input
	d.output = output

	return nil
}

// appendExecutionProvider adds the best available acceleration backend.
func appendExecutionProvider(options *ort.SessionOptions) {
	switch runtime.GOOS {
	case "darwin":
		// CoreML for Apple Silicon / macOS
		err := options.AppendExecutionProviderCoreMLV2(map[string]string{})
		if err != nil {
			slog.Warn("CoreML not available, falling back to CPU", "error", err)
		} else {
			slog.Info("using CoreML execution provider")
		}
	case "linux":
		// Try CUDA first, fall back to CPU
		cudaOpts, err := ort.NewCUDAProviderOptions()
		if err == nil {
			defer cudaOpts.Destroy()
			err = options.AppendExecutionProviderCUDA(cudaOpts)
			if err != nil {
				slog.Warn("CUDA not available, falling back to CPU", "error", err)
			} else {
				slog.Info("using CUDA execution provider")
			}
		} else {
			slog.Info("using CPU execution provider")
		}
	default:
		slog.Info("using CPU execution provider")
	}
}

// findOrtLibrary locates the ONNX Runtime shared library.
func findOrtLibrary() string {
	// Check environment variable first
	if p := os.Getenv("ORT_LIB_PATH"); p != "" {
		return p
	}

	// Platform-specific default locations
	candidates := ortLibraryCandidates()
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

func ortLibraryCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/usr/local/lib/libonnxruntime.dylib",
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"./third_party/libonnxruntime.dylib",
		}
	case "linux":
		return []string{
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so",
			"./third_party/libonnxruntime.so",
		}
	default:
		return nil
	}
}

func detectBackend() string {
	switch runtime.GOOS {
	case "darwin":
		return "CoreML (macOS)"
	case "linux":
		return "CUDA/CPU (Linux)"
	default:
		return "CPU"
	}
}
