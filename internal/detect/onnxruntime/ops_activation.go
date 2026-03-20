package onnxruntime

import (
	"fmt"
	"math"
)

func init() {
	Register("Sigmoid", opSigmoid)
	Register("Relu", opRelu)
	Register("Softmax", opSoftmax)
}

func opSigmoid(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("Sigmoid requires 1 input, got %d", len(inputs))
	}
	x := inputs[0]
	out := NewTensor(x.Shape, nil)

	for i, v := range x.Data {
		// Clamp to avoid overflow in exp()
		clamped := v
		if clamped < -88 {
			clamped = -88
		} else if clamped > 88 {
			clamped = 88
		}
		out.Data[i] = 1.0 / (1.0 + float32(math.Exp(float64(-clamped))))
	}

	return []*Tensor{out}, nil
}

func opRelu(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("Relu requires 1 input, got %d", len(inputs))
	}
	x := inputs[0]
	out := NewTensor(x.Shape, nil)

	for i, v := range x.Data {
		if v > 0 {
			out.Data[i] = v
		}
	}

	return []*Tensor{out}, nil
}

func opSoftmax(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("Softmax requires 1 input, got %d", len(inputs))
	}
	x := inputs[0]
	axis := int(attrs.GetInt("axis", -1))

	ndim := len(x.Shape)
	if axis < 0 {
		axis += ndim
	}
	if axis < 0 || axis >= ndim {
		return nil, fmt.Errorf("Softmax: axis %d out of range for %dD tensor", axis, ndim)
	}

	out := NewTensor(x.Shape, nil)
	copy(out.Data, x.Data)

	axisSize := int(x.Shape[axis])

	// Compute stride for the softmax axis
	innerSize := 1
	for i := axis + 1; i < ndim; i++ {
		innerSize *= int(x.Shape[i])
	}

	outerSize := len(x.Data) / (axisSize * innerSize)

	for outer := 0; outer < outerSize; outer++ {
		for inner := 0; inner < innerSize; inner++ {
			base := outer*axisSize*innerSize + inner

			// Find max for numerical stability
			maxVal := float32(math.Inf(-1))
			for a := 0; a < axisSize; a++ {
				idx := base + a*innerSize
				if out.Data[idx] > maxVal {
					maxVal = out.Data[idx]
				}
			}

			// Exp and sum
			sum := float32(0)
			for a := 0; a < axisSize; a++ {
				idx := base + a*innerSize
				out.Data[idx] = float32(math.Exp(float64(out.Data[idx] - maxVal)))
				sum += out.Data[idx]
			}

			// Normalize
			for a := 0; a < axisSize; a++ {
				idx := base + a*innerSize
				out.Data[idx] /= sum
			}
		}
	}

	return []*Tensor{out}, nil
}
