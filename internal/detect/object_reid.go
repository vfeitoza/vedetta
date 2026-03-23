package detect

import (
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/detect/onnxruntime"
)

const (
	reidInputH = 256
	reidInputW = 128
)

// ImageNet normalization constants.
var (
	reidMean = [3]float32{0.485, 0.456, 0.406}
	reidStd  = [3]float32{0.229, 0.224, 0.225}
)

// ObjectEmbedder generates embeddings for object re-identification using OSNet.
type ObjectEmbedder struct {
	mu         sync.Mutex
	session    *onnxruntime.Session
	inputName  string
	outputName string
	inputBuf   []float32
}

// ObjectEmbedderConfig configures the object embedding model.
type ObjectEmbedderConfig struct {
	ModelPath string // optional explicit path to ONNX model
}

// NewObjectEmbedder creates an OSNet-based object embedder.
func NewObjectEmbedder(cfg ObjectEmbedderConfig) (*ObjectEmbedder, error) {
	data, err := loadFaceModel(cfg.ModelPath, osnetFileName, downloadOSNet)
	if err != nil {
		return nil, fmt.Errorf("load OSNet model: %w", err)
	}

	session, err := onnxruntime.NewSession(data)
	if err != nil {
		return nil, fmt.Errorf("create OSNet session: %w", err)
	}

	inputNames := session.InputNames()
	outputNames := session.OutputNames()
	if len(inputNames) == 0 || len(outputNames) == 0 {
		return nil, fmt.Errorf("OSNet model has no inputs or outputs")
	}

	slog.Info("object re-ID model loaded",
		"input", inputNames[0],
		"output", outputNames[0],
	)

	return &ObjectEmbedder{
		session:    session,
		inputName:  inputNames[0],
		outputName: outputNames[0],
	}, nil
}

// Embed generates a 512-dim L2-normalized embedding for the object in the given
// bounding box. Returns the embedding vector.
func (oe *ObjectEmbedder) Embed(frame *image.RGBA, box [4]int) (embedding []float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("object re-ID panic recovered", "error", r)
			embedding = nil
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	oe.mu.Lock()
	defer oe.mu.Unlock()

	crop := cropRegion(frame, box)
	if crop.Bounds().Dx() < 16 || crop.Bounds().Dy() < 16 {
		return nil, fmt.Errorf("crop too small: %dx%d", crop.Bounds().Dx(), crop.Bounds().Dy())
	}

	input := oe.prepareInput(crop)

	inputTensor := onnxruntime.NewTensor(
		[]int64{1, 3, reidInputH, reidInputW}, input,
	)

	inputs := map[string]*onnxruntime.Tensor{
		oe.inputName: inputTensor,
	}

	outputs, err := oe.session.Run(inputs)
	if err != nil {
		return nil, fmt.Errorf("OSNet inference: %w", err)
	}

	outTensor := outputs[oe.outputName]
	if outTensor == nil || len(outTensor.Data) == 0 {
		return nil, fmt.Errorf("empty OSNet output")
	}

	// L2-normalize
	emb := make([]float32, len(outTensor.Data))
	copy(emb, outTensor.Data)
	var norm float64
	for _, v := range emb {
		norm += float64(v) * float64(v)
	}
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range emb {
			emb[i] *= invNorm
		}
	}

	return emb, nil
}

// SaveCrop saves a JPEG crop of the object bounding box and returns the file path.
func (oe *ObjectEmbedder) SaveCrop(frame *image.RGBA, box [4]int, dir string, objectID int64) string {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("create object crop dir", "error", err)
		return ""
	}

	crop := cropRegion(frame, box)
	filename := fmt.Sprintf("object_%d_%d.jpg", objectID, time.Now().UnixNano())
	path := filepath.Join(dir, filename)

	f, err := os.Create(path)
	if err != nil {
		slog.Error("create object crop file", "error", err)
		return ""
	}
	defer f.Close()

	if err := jpeg.Encode(f, crop, &jpeg.Options{Quality: 90}); err != nil {
		slog.Error("encode object crop", "error", err)
		os.Remove(path)
		return ""
	}
	return path
}

// Close releases model resources.
func (oe *ObjectEmbedder) Close() {}

// prepareInput resizes the crop to 256x128 and normalizes with ImageNet stats.
// Output format: NCHW float32 [1, 3, 256, 128].
func (oe *ObjectEmbedder) prepareInput(img *image.RGBA) []float32 {
	if oe.inputBuf == nil {
		oe.inputBuf = make([]float32, 3*reidInputH*reidInputW)
	}
	buf := oe.inputBuf

	bounds := img.Bounds()
	srcW := float64(bounds.Dx())
	srcH := float64(bounds.Dy())

	channelStride := reidInputH * reidInputW

	for y := range reidInputH {
		srcY := int(float64(y) * srcH / float64(reidInputH))
		if srcY >= bounds.Dy() {
			srcY = bounds.Dy() - 1
		}
		for x := range reidInputW {
			srcX := int(float64(x) * srcW / float64(reidInputW))
			if srcX >= bounds.Dx() {
				srcX = bounds.Dx() - 1
			}

			srcIdx := srcY*img.Stride + srcX*4
			r := (float32(img.Pix[srcIdx+0])/255.0 - reidMean[0]) / reidStd[0]
			g := (float32(img.Pix[srcIdx+1])/255.0 - reidMean[1]) / reidStd[1]
			b := (float32(img.Pix[srcIdx+2])/255.0 - reidMean[2]) / reidStd[2]

			dstIdx := y*reidInputW + x
			buf[0*channelStride+dstIdx] = r
			buf[1*channelStride+dstIdx] = g
			buf[2*channelStride+dstIdx] = b
		}
	}

	return buf
}
