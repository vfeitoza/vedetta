package onnxruntime

import (
	"fmt"
	"math"
)

func init() {
	Register("BatchNormalization", opBatchNorm)
}

func opBatchNorm(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 5 {
		return nil, fmt.Errorf("BatchNormalization requires 5 inputs, got %d", len(inputs))
	}

	x := inputs[0]
	scale := inputs[1]
	bias := inputs[2]
	mean := inputs[3]
	variance := inputs[4]

	if len(x.Shape) < 2 {
		return nil, fmt.Errorf("batchNormalization: input must be at least 2D, got %dD", len(x.Shape))
	}

	epsilon := attrs.GetFloat("epsilon", 1e-5)

	c := int(x.Shape[1])
	if len(scale.Data) != c || len(bias.Data) != c || len(mean.Data) != c || len(variance.Data) != c {
		return nil, fmt.Errorf("batchNormalization: channel dimension mismatch")
	}

	// Pre-compute per-channel coefficients: a = scale / sqrt(var + eps), b = bias - mean * a
	coeffA := make([]float32, c)
	coeffB := make([]float32, c)
	for i := 0; i < c; i++ {
		invStd := float32(1.0 / math.Sqrt(float64(variance.Data[i]+epsilon)))
		coeffA[i] = scale.Data[i] * invStd
		coeffB[i] = bias.Data[i] - mean.Data[i]*coeffA[i]
	}

	out := NewTensor(x.Shape, nil)

	// Spatial size = product of dimensions after channel
	spatialSize := 1
	for i := 2; i < len(x.Shape); i++ {
		spatialSize *= int(x.Shape[i])
	}

	n := int(x.Shape[0])
	for batch := 0; batch < n; batch++ {
		for ch := 0; ch < c; ch++ {
			offset := (batch*c + ch) * spatialSize
			a := coeffA[ch]
			b := coeffB[ch]
			for s := 0; s < spatialSize; s++ {
				idx := offset + s
				out.Data[idx] = a*x.Data[idx] + b
			}
		}
	}

	return []*Tensor{out}, nil
}
