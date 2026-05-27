package onnxruntime

import (
	"math"
	"testing"
)

func TestNewTensorNilData(t *testing.T) {
	tensor := NewTensor([]int64{2, 3}, nil)
	if len(tensor.Data) != 6 {
		t.Errorf("expected 6 elements, got %d", len(tensor.Data))
	}
	for i, v := range tensor.Data {
		if v != 0 {
			t.Errorf("data[%d] = %f, want 0", i, v)
		}
	}
}

func TestNewTensorWithData(t *testing.T) {
	data := []float32{1, 2, 3, 4}
	tensor := NewTensor([]int64{2, 2}, data)
	if &tensor.Data[0] != &data[0] {
		t.Error("expected tensor to use provided data slice")
	}
}

func TestScalarTensor(t *testing.T) {
	s := ScalarTensor(42)
	if len(s.Shape) != 0 {
		t.Errorf("scalar shape: got %v, want []", s.Shape)
	}
	if s.Size() != 1 {
		t.Errorf("scalar size: got %d, want 1", s.Size())
	}
	if s.Data[0] != 42 {
		t.Errorf("scalar value: got %f, want 42", s.Data[0])
	}
}

func TestTensorSize(t *testing.T) {
	tests := []struct {
		shape []int64
		want  int
	}{
		{[]int64{}, 1},         // scalar
		{[]int64{5}, 5},        // vector
		{[]int64{2, 3}, 6},     // matrix
		{[]int64{2, 3, 4}, 24}, // 3D
	}
	for _, tt := range tests {
		tensor := NewTensor(tt.shape, nil)
		if tensor.Size() != tt.want {
			t.Errorf("size of shape %v: got %d, want %d", tt.shape, tensor.Size(), tt.want)
		}
	}
}

func TestTensorDims(t *testing.T) {
	tests := []struct {
		shape []int64
		want  int
	}{
		{[]int64{}, 0},
		{[]int64{5}, 1},
		{[]int64{2, 3}, 2},
		{[]int64{1, 2, 3, 4}, 4},
	}
	for _, tt := range tests {
		tensor := NewTensor(tt.shape, nil)
		if tensor.Dims() != tt.want {
			t.Errorf("dims of shape %v: got %d, want %d", tt.shape, tensor.Dims(), tt.want)
		}
	}
}

func TestTensorClone(t *testing.T) {
	orig := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	clone := orig.Clone()

	// Values should match
	for i := range orig.Data {
		if clone.Data[i] != orig.Data[i] {
			t.Errorf("clone data[%d] mismatch", i)
		}
	}

	// Mutation should not affect original
	clone.Data[0] = 99
	if orig.Data[0] == 99 {
		t.Error("clone should be independent of original")
	}

	clone.Shape[0] = 10
	if orig.Shape[0] == 10 {
		t.Error("clone shape should be independent of original")
	}
}

func TestTensorStrides(t *testing.T) {
	tensor := NewTensor([]int64{2, 3, 4}, nil)
	strides := tensor.Strides()
	// [2,3,4] → strides [12, 4, 1]
	if strides[0] != 12 || strides[1] != 4 || strides[2] != 1 {
		t.Errorf("strides: got %v, want [12,4,1]", strides)
	}
}

func TestTensorStridesScalar(t *testing.T) {
	tensor := ScalarTensor(1)
	strides := tensor.Strides()
	if len(strides) != 0 {
		t.Errorf("scalar strides: got %v, want []", strides)
	}
}

func TestTensorReshape(t *testing.T) {
	tensor := NewTensor([]int64{2, 6}, make([]float32, 12))
	reshaped, err := tensor.Reshape([]int64{3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if reshaped.Shape[0] != 3 || reshaped.Shape[1] != 4 {
		t.Errorf("got shape %v, want [3,4]", reshaped.Shape)
	}
	// Should share underlying data
	tensor.Data[0] = 42
	if reshaped.Data[0] != 42 {
		t.Error("reshape should share data")
	}
}

func TestTensorReshapeInfer(t *testing.T) {
	tensor := NewTensor([]int64{2, 6}, make([]float32, 12))
	reshaped, err := tensor.Reshape([]int64{-1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if reshaped.Shape[0] != 4 || reshaped.Shape[1] != 3 {
		t.Errorf("got shape %v, want [4,3]", reshaped.Shape)
	}
}

func TestTensorReshapeIncompatible(t *testing.T) {
	tensor := NewTensor([]int64{2, 3}, make([]float32, 6))
	_, err := tensor.Reshape([]int64{4, 4})
	if err == nil {
		t.Error("expected error for incompatible reshape")
	}
}

func TestTensorReshapeMultipleInfer(t *testing.T) {
	tensor := NewTensor([]int64{2, 3}, make([]float32, 6))
	_, err := tensor.Reshape([]int64{-1, -1})
	if err == nil {
		t.Error("expected error for multiple -1 dimensions")
	}
}

func TestTensorTranspose2D(t *testing.T) {
	tensor := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	transposed := tensor.Transpose([]int64{1, 0})
	if transposed.Shape[0] != 3 || transposed.Shape[1] != 2 {
		t.Fatalf("transpose shape: got %v, want [3,2]", transposed.Shape)
	}
	want := []float32{1, 4, 2, 5, 3, 6}
	for i, v := range want {
		if transposed.Data[i] != v {
			t.Errorf("transpose[%d]: got %f, want %f", i, transposed.Data[i], v)
		}
	}
}

func TestTensorTranspose3D(t *testing.T) {
	tensor := NewTensor([]int64{2, 3, 4}, make([]float32, 24))
	for i := range tensor.Data {
		tensor.Data[i] = float32(i)
	}

	// Transpose [2,3,4] → [4,3,2] with perm [2,1,0]
	transposed := tensor.Transpose([]int64{2, 1, 0})
	if transposed.Shape[0] != 4 || transposed.Shape[1] != 3 || transposed.Shape[2] != 2 {
		t.Fatalf("3D transpose shape: got %v, want [4,3,2]", transposed.Shape)
	}

	// Verify: original[0][0][0]=0, transposed[0][0][0] should be 0
	if transposed.Data[0] != 0 {
		t.Errorf("transposed[0,0,0]: got %f, want 0", transposed.Data[0])
	}
	// original[1][2][3]=23, transposed[3][2][1] should be 23
	// transposed index: 3*6 + 2*2 + 1 = 23
	if transposed.Data[23] != 23 {
		t.Errorf("transposed[3,2,1]: got %f, want 23", transposed.Data[23])
	}
}

func TestTensorFill(t *testing.T) {
	tensor := NewTensor([]int64{3}, nil)
	tensor.Fill(7)
	for i, v := range tensor.Data {
		if v != 7 {
			t.Errorf("fill[%d]: got %f, want 7", i, v)
		}
	}
}

func TestTensorMax(t *testing.T) {
	tensor := NewTensor([]int64{5}, []float32{3, 1, 4, 1, 5})
	if tensor.Max() != 5 {
		t.Errorf("max: got %f, want 5", tensor.Max())
	}
}

func TestTensorMaxNegative(t *testing.T) {
	tensor := NewTensor([]int64{3}, []float32{-5, -3, -1})
	if tensor.Max() != -1 {
		t.Errorf("max negative: got %f, want -1", tensor.Max())
	}
}

func TestTensorMaxEmpty(t *testing.T) {
	tensor := &Tensor{Data: nil, Shape: []int64{0}}
	m := tensor.Max()
	if !math.IsInf(float64(m), -1) {
		t.Errorf("max empty: got %f, want -Inf", m)
	}
}

func TestBroadcastShapesSame(t *testing.T) {
	result, err := broadcastShapes([]int64{2, 3}, []int64{2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if result[0] != 2 || result[1] != 3 {
		t.Errorf("same shape broadcast: got %v", result)
	}
}

func TestBroadcastShapesScalar(t *testing.T) {
	result, err := broadcastShapes([]int64{2, 3}, []int64{})
	if err != nil {
		t.Fatal(err)
	}
	if result[0] != 2 || result[1] != 3 {
		t.Errorf("scalar broadcast: got %v", result)
	}
}

func TestBroadcastShapes1D(t *testing.T) {
	result, err := broadcastShapes([]int64{2, 3}, []int64{3})
	if err != nil {
		t.Fatal(err)
	}
	if result[0] != 2 || result[1] != 3 {
		t.Errorf("1D broadcast: got %v", result)
	}
}

func TestBroadcastShapesOneDim(t *testing.T) {
	result, err := broadcastShapes([]int64{2, 1}, []int64{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if result[0] != 2 || result[1] != 3 {
		t.Errorf("one-dim broadcast: got %v", result)
	}
}

func TestBroadcastShapesIncompatible(t *testing.T) {
	_, err := broadcastShapes([]int64{2, 3}, []int64{4})
	if err == nil {
		t.Fatal("expected error for incompatible shapes [2,3] and [4]")
	}
}

func TestBroadcastIndex(t *testing.T) {
	outShape := []int64{2, 3}
	inShape := []int64{3}

	// For output index (1,2) → flat index 5, input should map to index 2
	idx := broadcastIndex(5, outShape, inShape)
	if idx != 2 {
		t.Errorf("broadcast index: got %d, want 2", idx)
	}

	// For output index (0,0) → flat 0, input should map to 0
	idx = broadcastIndex(0, outShape, inShape)
	if idx != 0 {
		t.Errorf("broadcast index [0,0]: got %d, want 0", idx)
	}
}

func TestBroadcastIndexScalar(t *testing.T) {
	outShape := []int64{2, 3}
	inShape := []int64{}

	// Scalar always maps to index 0
	for i := int64(0); i < 6; i++ {
		idx := broadcastIndex(i, outShape, inShape)
		if idx != 0 {
			t.Errorf("scalar broadcast at %d: got %d, want 0", i, idx)
		}
	}
}
