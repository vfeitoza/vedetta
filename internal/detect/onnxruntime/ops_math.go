package onnxruntime

import "fmt"

func init() {
	Register("Add", opAdd)
	Register("Sub", opSub)
	Register("Mul", opMul)
	Register("Div", opDiv)
	Register("MatMul", opMatMul)
}

func binaryOp(inputs []*Tensor, op func(a, b float32) float32) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("binary op requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

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

func opAdd(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	return binaryOp(inputs, func(a, b float32) float32 { return a + b })
}

func opSub(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	return binaryOp(inputs, func(a, b float32) float32 { return a - b })
}

func opMul(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	return binaryOp(inputs, func(a, b float32) float32 { return a * b })
}

func opDiv(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	return binaryOp(inputs, func(a, b float32) float32 { return a / b })
}

func opMatMul(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("MatMul requires 2 inputs, got %d", len(inputs))
	}
	a, b := inputs[0], inputs[1]

	if a.Dims() < 2 || b.Dims() < 2 {
		return nil, fmt.Errorf("MatMul requires at least 2D inputs, got %dD and %dD", a.Dims(), b.Dims())
	}

	aShape := a.Shape
	bShape := b.Shape

	m := int(aShape[len(aShape)-2])
	k := int(aShape[len(aShape)-1])
	n := int(bShape[len(bShape)-1])

	if int(bShape[len(bShape)-2]) != k {
		return nil, fmt.Errorf("MatMul: inner dimensions mismatch %d vs %d", k, bShape[len(bShape)-2])
	}

	// Compute batch dimensions
	aBatch := aShape[:len(aShape)-2]
	bBatch := bShape[:len(bShape)-2]

	batchShape, err := broadcastShapes(aBatch, bBatch)
	if err != nil {
		return nil, fmt.Errorf("MatMul: %w", err)
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
	}

	return []*Tensor{out}, nil
}
