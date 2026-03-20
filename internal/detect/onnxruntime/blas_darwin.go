//go:build darwin

package onnxruntime

// #cgo CFLAGS: -DACCELERATE_NEW_LAPACK
// #cgo LDFLAGS: -framework Accelerate
// #include <Accelerate/Accelerate.h>
import "C"

// sgemm uses Apple's Accelerate framework for SIMD-optimized matrix multiply.
// C = A × B where A is (m×k), B is (k×n), C is (m×n).
func sgemm(a []float32, b []float32, m, n, k int) []float32 {
	c := make([]float32, m*n)
	if m == 0 || n == 0 || k == 0 {
		return c
	}
	C.cblas_sgemm(
		C.CblasRowMajor, C.CblasNoTrans, C.CblasNoTrans,
		C.int(m), C.int(n), C.int(k),
		1.0,
		(*C.float)(&a[0]), C.int(k),
		(*C.float)(&b[0]), C.int(n),
		0.0,
		(*C.float)(&c[0]), C.int(n),
	)
	return c
}
