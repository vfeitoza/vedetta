//go:build darwin

package onnxruntime

// #cgo CFLAGS: -DACCELERATE_NEW_LAPACK
// #cgo LDFLAGS: -framework Accelerate
// #include <Accelerate/Accelerate.h>
import "C"

// sgemm uses Apple's Accelerate framework for SIMD-optimized matrix multiply.
// For small matrices, falls back to pure Go to avoid CGo overhead.
func sgemm(a []float32, b []float32, m, n, k int) []float32 {
	c := getGemmBuffer(m * n)
	if m == 0 || n == 0 || k == 0 {
		return c
	}
	if m*n < sgemmThreshold {
		sgemmPureGo(a, b, c, m, n, k)
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

// sgemmInto writes the result directly into the provided output buffer.
func sgemmInto(a []float32, b []float32, c []float32, m, n, k int) {
	if m == 0 || n == 0 || k == 0 {
		for i := range c[:m*n] {
			c[i] = 0
		}
		return
	}
	if m*n < sgemmThreshold {
		sgemmPureGo(a, b, c, m, n, k)
		return
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
}
