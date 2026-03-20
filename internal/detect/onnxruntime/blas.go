package onnxruntime

import "sync"

// gemmFreeList reuses GEMM output buffers across GC cycles.
var gemmFreeList struct {
	mu   sync.Mutex
	bufs [][]float32
}

func getGemmBuffer(size int) []float32 {
	gemmFreeList.mu.Lock()
	for i := len(gemmFreeList.bufs) - 1; i >= 0; i-- {
		buf := gemmFreeList.bufs[i]
		if cap(buf) >= size {
			gemmFreeList.bufs = append(gemmFreeList.bufs[:i], gemmFreeList.bufs[i+1:]...)
			gemmFreeList.mu.Unlock()
			buf = buf[:size]
			for j := range buf {
				buf[j] = 0
			}
			return buf
		}
	}
	gemmFreeList.mu.Unlock()
	return make([]float32, size)
}

func putGemmBuffer(buf []float32) {
	gemmFreeList.mu.Lock()
	gemmFreeList.bufs = append(gemmFreeList.bufs, buf)
	gemmFreeList.mu.Unlock()
}

// Sgemm performs single-precision general matrix multiplication:
//
//	C = A × B
//
// where A is (m × k), B is (k × n), C is (m × n).
// All matrices are in row-major order.
//
// On macOS, this dispatches to Apple's Accelerate framework (NEON SIMD)
// for large matrices, falling back to pure Go for small ones where CGo
// overhead would dominate.
func Sgemm(a []float32, b []float32, m, n, k int) []float32 {
	return sgemm(a, b, m, n, k)
}

// SgemmInto writes the result of A × B directly into the provided output slice c.
// c must have length >= m*n.
func SgemmInto(a []float32, b []float32, c []float32, m, n, k int) {
	sgemmInto(a, b, c, m, n, k)
}

// sgemmThreshold is the minimum total output elements (m*n) below which
// pure Go GEMM is used to avoid CGo call overhead.
const sgemmThreshold = 512

// sgemmPureGo performs matrix multiplication in pure Go with tiled loops.
// Zeroes the output buffer before accumulating.
func sgemmPureGo(a []float32, b []float32, c []float32, m, n, k int) {
	for i := range c[:m*n] {
		c[i] = 0
	}

	const tileSize = 64

	for ii := 0; ii < m; ii += tileSize {
		iEnd := min(ii+tileSize, m)
		for kk := 0; kk < k; kk += tileSize {
			kEnd := min(kk+tileSize, k)
			for jj := 0; jj < n; jj += tileSize {
				jEnd := min(jj+tileSize, n)
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
}
