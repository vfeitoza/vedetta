package detect

import (
	"fmt"
	"runtime"

	"github.com/rvben/vedetta/internal/detect/onnxruntime"
)

// GoBackend is the pure Go ONNX inference engine. It requires no external
// dependencies and works on every platform Go supports.
//
// Not safe for concurrent use. Each goroutine needs its own instance.
type GoBackend struct {
	session   *onnxruntime.Session
	inputMap  map[string]*onnxruntime.Tensor
	inputKey  string
	outputKey string
}

// NewGoBackend loads an ONNX model and returns a pure Go inference backend.
func NewGoBackend(modelData []byte) (*GoBackend, error) {
	session, err := onnxruntime.NewSession(modelData)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	inputNames := session.InputNames()
	outputNames := session.OutputNames()
	if len(inputNames) == 0 || len(outputNames) == 0 {
		return nil, fmt.Errorf("model has no inputs or outputs")
	}

	inputKey := inputNames[0]
	b := &GoBackend{
		session:   session,
		inputKey:  inputKey,
		outputKey: outputNames[0],
		inputMap:  map[string]*onnxruntime.Tensor{inputKey: nil},
	}
	return b, nil
}

// Run executes inference using the pure Go ONNX runtime.
func (b *GoBackend) Run(input []float32) ([]float32, error) {
	if len(input) != inputTensorSize {
		return nil, fmt.Errorf("input size %d, want %d", len(input), inputTensorSize)
	}

	inputTensor := onnxruntime.NewTensor(
		[]int64{1, 3, modelInputSize, modelInputSize}, input,
	)

	// Reuse the map — only update the value pointer.
	b.inputMap[b.inputKey] = inputTensor

	outputs, err := b.session.Run(b.inputMap)
	if err != nil {
		return nil, err
	}

	output, ok := outputs[b.outputKey]
	if !ok {
		return nil, fmt.Errorf("model produced no %q tensor", b.outputKey)
	}

	return output.Data, nil
}

// Close is a no-op for the pure Go backend (no external resources).
func (b *GoBackend) Close() {}

// Name returns the backend identifier including BLAS info.
func (b *GoBackend) Name() string {
	if runtime.GOOS == "darwin" {
		return "pure Go + Apple Accelerate BLAS"
	}
	return "pure Go"
}
