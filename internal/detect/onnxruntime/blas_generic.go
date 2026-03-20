//go:build !darwin

package onnxruntime

// sgemm performs matrix multiplication in pure Go.
// C = A × B where A is (m×k), B is (k×n), C is (m×n).
func sgemm(a []float32, b []float32, m, n, k int) []float32 {
	c := getGemmBuffer(m * n)
	if m == 0 || n == 0 || k == 0 {
		return c
	}
	sgemmPureGo(a, b, c, m, n, k)
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
	sgemmPureGo(a, b, c, m, n, k)
}
