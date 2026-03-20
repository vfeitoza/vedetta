//go:build !darwin

package onnxruntime

// sgemm performs matrix multiplication in pure Go.
// C = A × B where A is (m×k), B is (k×n), C is (m×n).
// Uses loop tiling for better cache locality.
func sgemm(a []float32, b []float32, m, n, k int) []float32 {
	c := make([]float32, m*n)
	if m == 0 || n == 0 || k == 0 {
		return c
	}

	const tileSize = 64

	for ii := 0; ii < m; ii += tileSize {
		iEnd := ii + tileSize
		if iEnd > m {
			iEnd = m
		}
		for kk := 0; kk < k; kk += tileSize {
			kEnd := kk + tileSize
			if kEnd > k {
				kEnd = k
			}
			for jj := 0; jj < n; jj += tileSize {
				jEnd := jj + tileSize
				if jEnd > n {
					jEnd = n
				}
				for i := ii; i < iEnd; i++ {
					for p := kk; p < kEnd; p++ {
						aip := a[i*k+p]
						for j := jj; j < jEnd; j++ {
							c[i*n+j] += aip * b[p*n+j]
						}
					}
				}
			}
		}
	}
	return c
}
