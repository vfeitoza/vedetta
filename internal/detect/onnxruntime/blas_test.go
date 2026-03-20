package onnxruntime

import (
	"math"
	"testing"
)

func approxEqual(a, b, eps float32) bool {
	return float32(math.Abs(float64(a-b))) < eps
}

func assertTensorApprox(t *testing.T, got *Tensor, wantShape []int64, wantData []float32, eps float32) {
	t.Helper()
	if len(got.Shape) != len(wantShape) {
		t.Fatalf("shape dims: got %v, want %v", got.Shape, wantShape)
	}
	for i := range wantShape {
		if got.Shape[i] != wantShape[i] {
			t.Fatalf("shape[%d]: got %d, want %d", i, got.Shape[i], wantShape[i])
		}
	}
	if len(got.Data) != len(wantData) {
		t.Fatalf("data len: got %d, want %d", len(got.Data), len(wantData))
	}
	for i := range wantData {
		if !approxEqual(got.Data[i], wantData[i], eps) {
			t.Errorf("data[%d]: got %f, want %f", i, got.Data[i], wantData[i])
		}
	}
}

func TestSgemmIdentity(t *testing.T) {
	// A × I = A
	a := []float32{1, 2, 3, 4, 5, 6}
	identity := []float32{1, 0, 0, 0, 1, 0, 0, 0, 1}
	result := Sgemm(a, identity, 2, 3, 3)
	for i, v := range a {
		if !approxEqual(result[i], v, 1e-6) {
			t.Errorf("result[%d] = %f, want %f", i, result[i], v)
		}
	}
}

func TestSgemmKnownResult(t *testing.T) {
	// [1,2; 3,4] × [5,6; 7,8] = [19,22; 43,50]
	a := []float32{1, 2, 3, 4}
	b := []float32{5, 6, 7, 8}
	result := Sgemm(a, b, 2, 2, 2)
	want := []float32{19, 22, 43, 50}
	for i, v := range want {
		if !approxEqual(result[i], v, 1e-5) {
			t.Errorf("result[%d] = %f, want %f", i, result[i], v)
		}
	}
}

func TestSgemmNonSquare(t *testing.T) {
	// [1,2,3] × [4;5;6] = [32] (1x3 × 3x1 = 1x1)
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	result := Sgemm(a, b, 1, 1, 3)
	if !approxEqual(result[0], 32, 1e-5) {
		t.Errorf("result = %f, want 32", result[0])
	}
}

func TestSgemmLarger(t *testing.T) {
	// 3x2 × 2x4
	a := []float32{1, 2, 3, 4, 5, 6}
	b := []float32{1, 2, 3, 4, 5, 6, 7, 8}
	result := Sgemm(a, b, 3, 4, 2)
	// Row 0: [1*1+2*5, 1*2+2*6, 1*3+2*7, 1*4+2*8] = [11, 14, 17, 20]
	// Row 1: [3*1+4*5, 3*2+4*6, 3*3+4*7, 3*4+4*8] = [23, 30, 37, 44]
	// Row 2: [5*1+6*5, 5*2+6*6, 5*3+6*7, 5*4+6*8] = [35, 46, 57, 68]
	want := []float32{11, 14, 17, 20, 23, 30, 37, 44, 35, 46, 57, 68}
	for i, v := range want {
		if !approxEqual(result[i], v, 1e-4) {
			t.Errorf("result[%d] = %f, want %f", i, result[i], v)
		}
	}
}

func TestSgemmEmpty(t *testing.T) {
	result := Sgemm(nil, nil, 0, 0, 0)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func BenchmarkSgemm256(b *testing.B) {
	n := 256
	a := make([]float32, n*n)
	bm := make([]float32, n*n)
	for i := range a {
		a[i] = float32(i) * 0.001
		bm[i] = float32(i) * 0.001
	}
	b.ResetTimer()
	for range b.N {
		Sgemm(a, bm, n, n, n)
	}
}
