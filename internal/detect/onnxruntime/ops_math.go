package onnxruntime

import "fmt"

func init() {
	Register("Add", opAdd)
	Register("Sub", opSub)
	Register("Mul", opMul)
	Register("Div", opDiv)
	Register("MatMul", opMatMul)
}

// shapesEqual returns true if two shapes are identical.
func shapesEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isPerChannelBroadcast detects [N,C,H,W] op [C,1,1] or [1,C,1,1] patterns
// common in neural networks (bias add, batch norm scale).
// Returns (channelStride, channels, true) if the pattern matches.
func isPerChannelBroadcast(big, small []int64) (int, int, bool) {
	if len(big) != 4 {
		return 0, 0, false
	}
	// small must be [C,1,1] or [1,C,1,1]
	var c int64
	switch len(small) {
	case 3:
		if small[1] != 1 || small[2] != 1 {
			return 0, 0, false
		}
		c = small[0]
	case 4:
		if small[0] != 1 || small[2] != 1 || small[3] != 1 {
			return 0, 0, false
		}
		c = small[1]
	default:
		return 0, 0, false
	}
	if c != big[1] {
		return 0, 0, false
	}
	return int(big[2] * big[3]), int(c), true
}

func opAdd(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("binary op requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	// Fast path: same shape
	if shapesEqual(a.Shape, b.Shape) {
		out := newTensorUninit(a.Shape)
		for i := range out.Data {
			out.Data[i] = a.Data[i] + b.Data[i]
		}
		return []*Tensor{out}, nil
	}

	// Fast path: scalar broadcast
	if len(b.Shape) == 0 || (len(b.Data) == 1) {
		bv := b.Data[0]
		out := newTensorUninit(a.Shape)
		for i := range out.Data {
			out.Data[i] = a.Data[i] + bv
		}
		return []*Tensor{out}, nil
	}

	// Fast path: per-channel broadcast [N,C,H,W] + [C,1,1]
	if spatialSize, channels, ok := isPerChannelBroadcast(a.Shape, b.Shape); ok {
		out := newTensorUninit(a.Shape)
		n := int(a.Shape[0])
		idx := 0
		for ni := 0; ni < n; ni++ {
			for c := 0; c < channels; c++ {
				bv := b.Data[c]
				for s := 0; s < spatialSize; s++ {
					out.Data[idx] = a.Data[idx] + bv
					idx++
				}
			}
		}
		return []*Tensor{out}, nil
	}

	return binaryOpSlow(a, b, func(x, y float32) float32 { return x + y })
}

func opSub(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("binary op requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	if shapesEqual(a.Shape, b.Shape) {
		out := newTensorUninit(a.Shape)
		for i := range out.Data {
			out.Data[i] = a.Data[i] - b.Data[i]
		}
		return []*Tensor{out}, nil
	}

	return binaryOpSlow(a, b, func(x, y float32) float32 { return x - y })
}

func opMul(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("binary op requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	if shapesEqual(a.Shape, b.Shape) {
		out := newTensorUninit(a.Shape)
		for i := range out.Data {
			out.Data[i] = a.Data[i] * b.Data[i]
		}
		return []*Tensor{out}, nil
	}

	if len(b.Shape) == 0 || (len(b.Data) == 1) {
		bv := b.Data[0]
		out := newTensorUninit(a.Shape)
		for i := range out.Data {
			out.Data[i] = a.Data[i] * bv
		}
		return []*Tensor{out}, nil
	}

	if spatialSize, channels, ok := isPerChannelBroadcast(a.Shape, b.Shape); ok {
		out := newTensorUninit(a.Shape)
		n := int(a.Shape[0])
		idx := 0
		for ni := 0; ni < n; ni++ {
			for c := 0; c < channels; c++ {
				bv := b.Data[c]
				for s := 0; s < spatialSize; s++ {
					out.Data[idx] = a.Data[idx] * bv
					idx++
				}
			}
		}
		return []*Tensor{out}, nil
	}

	return binaryOpSlow(a, b, func(x, y float32) float32 { return x * y })
}

func opDiv(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("binary op requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	if shapesEqual(a.Shape, b.Shape) {
		out := newTensorUninit(a.Shape)
		for i := range out.Data {
			out.Data[i] = a.Data[i] / b.Data[i]
		}
		return []*Tensor{out}, nil
	}

	return binaryOpSlow(a, b, func(x, y float32) float32 { return x / y })
}

// binaryOpSlow is the generic fallback using broadcastIndex.
func binaryOpSlow(a, b *Tensor, op func(x, y float32) float32) ([]*Tensor, error) {
	outShape, err := broadcastShapes(a.Shape, b.Shape)
	if err != nil {
		return nil, err
	}

	size := int(tensorSize(outShape))
	out := NewTensor(outShape, nil)

	for i := 0; i < size; i++ {
		ai := broadcastIndex(int64(i), outShape, a.Shape)
		bi := broadcastIndex(int64(i), outShape, b.Shape)
		out.Data[i] = op(a.Data[ai], b.Data[bi])
	}

	return []*Tensor{out}, nil
}

func opMatMul(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("matMul requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	if a.Dims() < 2 || b.Dims() < 2 {
		return nil, fmt.Errorf("matMul requires at least 2D inputs, got %dD and %dD", a.Dims(), b.Dims())
	}

	aShape := a.Shape
	bShape := b.Shape

	m := int(aShape[len(aShape)-2])
	k := int(aShape[len(aShape)-1])
	n := int(bShape[len(bShape)-1])

	if int(bShape[len(bShape)-2]) != k {
		return nil, fmt.Errorf("matMul: inner dimensions mismatch %d vs %d", k, bShape[len(bShape)-2])
	}

	// Compute batch dimensions
	aBatch := aShape[:len(aShape)-2]
	bBatch := bShape[:len(bShape)-2]

	batchShape, err := broadcastShapes(aBatch, bBatch)
	if err != nil {
		return nil, fmt.Errorf("matMul: %w", err)
	}

	batchSize := int(tensorSize(batchShape))
	aMatSize := m * k
	bMatSize := k * n
	outMatSize := m * n

	outShape := make([]int64, len(batchShape)+2)
	copy(outShape, batchShape)
	outShape[len(batchShape)] = int64(m)
	outShape[len(batchShape)+1] = int64(n)

	out := NewTensor(outShape, nil)

	for batch := 0; batch < batchSize; batch++ {
		ai := int(broadcastIndex(int64(batch), batchShape, aBatch)) * aMatSize
		bi := int(broadcastIndex(int64(batch), batchShape, bBatch)) * bMatSize
		oi := batch * outMatSize

		result := Sgemm(a.Data[ai:ai+aMatSize], b.Data[bi:bi+bMatSize], m, n, k)
		copy(out.Data[oi:oi+outMatSize], result)
		putGemmBuffer(result)
	}

	return []*Tensor{out}, nil
}
