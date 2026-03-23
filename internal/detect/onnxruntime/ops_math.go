package onnxruntime

import "fmt"

func init() {
	Register("Add", opAdd)
	Register("Sub", opSub)
	Register("Mul", opMul)
	Register("Div", opDiv)
	Register("MatMul", opMatMul)
	Register("Gemm", opGemm)
	Register("ReduceMean", opReduceMean)
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

// transposeMatrix2D returns a new row-major matrix with rows and columns swapped.
// src is (rows x cols), result is (cols x rows). Uses pooled buffer.
func transposeMatrix2D(src []float32, rows, cols int) []float32 {
	dst := getGemmBuffer(rows * cols)
	for i := range rows {
		for j := range cols {
			dst[j*rows+i] = src[i*cols+j]
		}
	}
	return dst
}

func opGemm(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("gemm requires at least 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	if len(a.Shape) != 2 || len(b.Shape) != 2 {
		return nil, fmt.Errorf("gemm requires 2D inputs, got %dD and %dD", len(a.Shape), len(b.Shape))
	}

	alpha := attrs.GetFloat("alpha", 1.0)
	beta := attrs.GetFloat("beta", 1.0)
	transA := attrs.GetInt("transA", 0) != 0
	transB := attrs.GetInt("transB", 0) != 0

	// Determine M, K from A (possibly transposed)
	aRows, aCols := int(a.Shape[0]), int(a.Shape[1])
	var m, k int
	if transA {
		m, k = aCols, aRows
	} else {
		m, k = aRows, aCols
	}

	// Determine K2, N from B (possibly transposed)
	bRows, bCols := int(b.Shape[0]), int(b.Shape[1])
	var k2, n int
	if transB {
		k2, n = bCols, bRows
	} else {
		k2, n = bRows, bCols
	}

	if k != k2 {
		return nil, fmt.Errorf("gemm: inner dimensions mismatch %d vs %d", k, k2)
	}

	// Prepare row-major A' and B' for Sgemm (which computes C = A' * B')
	aData := a.Data
	if transA {
		aData = transposeMatrix2D(a.Data, aRows, aCols)
	}
	bData := b.Data
	if transB {
		bData = transposeMatrix2D(b.Data, bRows, bCols)
	}

	// C = A' * B' via Sgemm
	result := Sgemm(aData, bData, m, n, k)

	// Return transpose buffers to pool
	if transA {
		putGemmBuffer(aData)
	}
	if transB {
		putGemmBuffer(bData)
	}

	// Apply alpha if not 1.0
	if alpha != 1.0 {
		for i := range result {
			result[i] *= alpha
		}
	}

	// Add beta * C (bias) if provided
	if len(inputs) > 2 && inputs[2] != nil && beta != 0 {
		c := inputs[2]
		if len(c.Shape) == 1 && c.Shape[0] == int64(n) {
			// 1D bias broadcast: add to each row
			for i := range m {
				for j := range n {
					result[i*n+j] += beta * c.Data[j]
				}
			}
		} else if len(c.Shape) == 2 && c.Shape[0] == int64(m) && c.Shape[1] == int64(n) {
			// 2D bias: element-wise add
			for i := range result {
				result[i] += beta * c.Data[i]
			}
		} else if len(c.Shape) == 0 || len(c.Data) == 1 {
			// Scalar bias
			bv := beta * c.Data[0]
			for i := range result {
				result[i] += bv
			}
		} else {
			putGemmBuffer(result)
			return nil, fmt.Errorf("gemm: unsupported bias shape %v for output [%d, %d]", c.Shape, m, n)
		}
	}

	// Copy result into properly-pooled tensor and return Sgemm buffer
	outShape := []int64{int64(m), int64(n)}
	out := NewTensor(outShape, nil)
	copy(out.Data, result)
	putGemmBuffer(result)
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

	for batch := range batchSize {
		ai := int(broadcastIndex(int64(batch), batchShape, aBatch)) * aMatSize
		bi := int(broadcastIndex(int64(batch), batchShape, bBatch)) * bMatSize
		oi := batch * outMatSize

		result := Sgemm(a.Data[ai:ai+aMatSize], b.Data[bi:bi+bMatSize], m, n, k)
		copy(out.Data[oi:oi+outMatSize], result)
		putGemmBuffer(result)
	}

	return []*Tensor{out}, nil
}

func opReduceMean(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 || inputs[0] == nil {
		return nil, fmt.Errorf("reducemean: need at least 1 input")
	}
	data := inputs[0]
	keepdims := attrs.GetInt("keepdims", 1)

	// Resolve axes: opset 18+ uses second input, older uses attribute
	var axes []int
	if len(inputs) > 1 && inputs[1] != nil && len(inputs[1].Data) > 0 {
		for _, v := range inputs[1].Data {
			ax := int(v)
			if ax < 0 {
				ax += len(data.Shape)
			}
			axes = append(axes, ax)
		}
	} else if axesAttr := attrs.GetIntList("axes"); len(axesAttr) > 0 {
		for _, v := range axesAttr {
			ax := int(v)
			if ax < 0 {
				ax += len(data.Shape)
			}
			axes = append(axes, ax)
		}
	} else {
		for i := range data.Shape {
			axes = append(axes, i)
		}
	}

	// Build a set of axes to reduce
	reduceSet := make(map[int]bool, len(axes))
	for _, ax := range axes {
		reduceSet[ax] = true
	}

	// Compute output shape
	ndim := len(data.Shape)
	var outShape []int64
	for d := 0; d < ndim; d++ {
		if reduceSet[d] {
			if keepdims != 0 {
				outShape = append(outShape, 1)
			}
		} else {
			outShape = append(outShape, data.Shape[d])
		}
	}
	if len(outShape) == 0 {
		outShape = []int64{1}
	}

	outSize := int64(1)
	for _, s := range outShape {
		outSize *= s
	}
	out := make([]float32, outSize)
	counts := make([]float32, outSize)

	// Compute strides for input
	strides := make([]int64, ndim)
	strides[ndim-1] = 1
	for d := ndim - 2; d >= 0; d-- {
		strides[d] = strides[d+1] * data.Shape[d+1]
	}

	// Compute strides for output (in terms of the non-reduced dims)
	outStrides := make([]int64, ndim)
	outDim := int64(1)
	for d := ndim - 1; d >= 0; d-- {
		if reduceSet[d] {
			outStrides[d] = 0
		} else {
			outStrides[d] = outDim
			outDim *= data.Shape[d]
		}
	}

	// Accumulate
	totalElems := int64(len(data.Data))
	for i := int64(0); i < totalElems; i++ {
		rem := i
		outIdx := int64(0)
		for d := 0; d < ndim; d++ {
			coord := rem / strides[d]
			rem %= strides[d]
			outIdx += coord * outStrides[d]
		}
		out[outIdx] += data.Data[i]
		counts[outIdx]++
	}

	for i := range out {
		if counts[i] > 0 {
			out[i] /= counts[i]
		}
	}

	return []*Tensor{{Data: out, Shape: outShape}}, nil
}
