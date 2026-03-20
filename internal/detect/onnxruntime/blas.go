package onnxruntime

// Sgemm performs single-precision general matrix multiplication:
//
//	C = A × B
//
// where A is (m × k), B is (k × n), C is (m × n).
// All matrices are in row-major order.
//
// On macOS, this dispatches to Apple's Accelerate framework (NEON SIMD).
// On other platforms, a pure Go implementation is used.
func Sgemm(a []float32, b []float32, m, n, k int) []float32 {
	return sgemm(a, b, m, n, k)
}
