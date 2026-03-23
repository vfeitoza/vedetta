package detect

import (
	"image"
	"image/color"
	"math"
)

// Canonical face landmark positions for InsightFace 112x112 alignment.
var canonicalLandmarks = [5][2]float64{
	{38.2946, 51.6963}, // left eye
	{73.5318, 51.5014}, // right eye
	{56.0252, 71.7366}, // nose
	{41.5493, 92.3655}, // left mouth
	{70.7299, 92.2041}, // right mouth
}

// affineMatrix represents a 2x3 affine transformation matrix.
// [a, b, tx]
// [c, d, ty]
type affineMatrix [6]float64

// estimateAffine computes a 2x3 affine matrix that maps src landmarks to dst landmarks
// using least-squares estimation from point correspondences.
// src and dst must each have at least 3 points.
func estimateAffine(src, dst [5][2]float64) affineMatrix {
	// Solve for M in dst = M * src (homogeneous coordinates)
	// Using normal equations: M = (dst * srcT) * (src * srcT)^-1
	//
	// Build the linear system: for each point pair (sx,sy) -> (dx,dy):
	//   dx = a*sx + b*sy + tx
	//   dy = c*sx + d*sy + ty
	//
	// This gives us two 3x3 systems:
	//   [sum(sx*sx) sum(sx*sy) sum(sx)] [a]   [sum(dx*sx)]
	//   [sum(sy*sx) sum(sy*sy) sum(sy)] [b] = [sum(dx*sy)]
	//   [sum(sx)    sum(sy)    n      ] [tx]  [sum(dx)   ]
	// (and similarly for c, d, ty with dy)

	n := float64(len(src))
	var sxx, sxy, syy, sx, sy float64
	var dxsx, dxsy, dxs float64
	var dysx, dysy, dys float64

	for i := range src {
		sxi, syi := src[i][0], src[i][1]
		dxi, dyi := dst[i][0], dst[i][1]

		sxx += sxi * sxi
		sxy += sxi * syi
		syy += syi * syi
		sx += sxi
		sy += syi

		dxsx += dxi * sxi
		dxsy += dxi * syi
		dxs += dxi

		dysx += dyi * sxi
		dysy += dyi * syi
		dys += dyi
	}

	// Solve 3x3 system using Cramer's rule
	// A = [[sxx, sxy, sx], [sxy, syy, sy], [sx, sy, n]]
	det := sxx*(syy*n-sy*sy) - sxy*(sxy*n-sy*sx) + sx*(sxy*sy-syy*sx)
	if math.Abs(det) < 1e-10 {
		// Degenerate: return identity
		return affineMatrix{1, 0, 0, 0, 1, 0}
	}

	invDet := 1.0 / det

	// Inverse of A (symmetric matrix)
	inv00 := (syy*n - sy*sy) * invDet
	inv01 := (sx*sy - sxy*n) * invDet
	inv02 := (sxy*sy - syy*sx) * invDet
	inv10 := inv01
	inv11 := (sxx*n - sx*sx) * invDet
	inv12 := (sxy*sx - sxx*sy) * invDet
	inv20 := inv02
	inv21 := inv12
	inv22 := (sxx*syy - sxy*sxy) * invDet

	a := inv00*dxsx + inv01*dxsy + inv02*dxs
	b := inv10*dxsx + inv11*dxsy + inv12*dxs
	tx := inv20*dxsx + inv21*dxsy + inv22*dxs

	c := inv00*dysx + inv01*dysy + inv02*dys
	d := inv10*dysx + inv11*dysy + inv12*dys
	ty := inv20*dysx + inv21*dysy + inv22*dys

	return affineMatrix{a, b, tx, c, d, ty}
}

// alignFace warps the source image to produce a 112x112 aligned face using the
// detected landmarks. The returned image is suitable for MobileFaceNet input.
func alignFace(src *image.RGBA, landmarks [5][2]float32) *image.RGBA {
	// Convert landmarks to float64
	var srcPts [5][2]float64
	for i := range landmarks {
		srcPts[i][0] = float64(landmarks[i][0])
		srcPts[i][1] = float64(landmarks[i][1])
	}

	// Estimate forward transform: src -> canonical (112x112)
	M := estimateAffine(srcPts, canonicalLandmarks)

	// Apply inverse warp: for each output pixel, find the source pixel
	// Inverse of [a, b, tx; c, d, ty] is computed via 2x2 inverse + translation
	det := M[0]*M[4] - M[1]*M[3]
	if math.Abs(det) < 1e-10 {
		// Degenerate transform — return a blank image
		return image.NewRGBA(image.Rect(0, 0, 112, 112))
	}
	invDet := 1.0 / det
	// Inverse of the 2x2 part
	ia := M[4] * invDet
	ib := -M[1] * invDet
	ic := -M[3] * invDet
	id := M[0] * invDet
	// Inverse translation
	itx := -(ia*M[2] + ib*M[5])
	ity := -(ic*M[2] + id*M[5])

	dst := image.NewRGBA(image.Rect(0, 0, 112, 112))
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	for y := range 112 {
		for x := range 112 {
			// Map output (x,y) back to source coordinates
			srcX := ia*float64(x) + ib*float64(y) + itx
			srcY := ic*float64(x) + id*float64(y) + ity

			// Bilinear interpolation
			sx0 := int(math.Floor(srcX))
			sy0 := int(math.Floor(srcY))
			fx := srcX - float64(sx0)
			fy := srcY - float64(sy0)

			if sx0 < 0 || sy0 < 0 || sx0+1 >= srcW || sy0+1 >= srcH {
				// Out of bounds — black pixel
				continue
			}

			// Sample 4 corners
			c00 := sampleRGBA(src, sx0, sy0)
			c10 := sampleRGBA(src, sx0+1, sy0)
			c01 := sampleRGBA(src, sx0, sy0+1)
			c11 := sampleRGBA(src, sx0+1, sy0+1)

			// Interpolate
			r := bilinear(float64(c00.R), float64(c10.R), float64(c01.R), float64(c11.R), fx, fy)
			g := bilinear(float64(c00.G), float64(c10.G), float64(c01.G), float64(c11.G), fx, fy)
			b := bilinear(float64(c00.B), float64(c10.B), float64(c01.B), float64(c11.B), fx, fy)

			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(clamp(r, 0, 255)),
				G: uint8(clamp(g, 0, 255)),
				B: uint8(clamp(b, 0, 255)),
				A: 255,
			})
		}
	}

	return dst
}

func sampleRGBA(img *image.RGBA, x, y int) color.RGBA {
	bounds := img.Bounds()
	idx := (y-bounds.Min.Y)*img.Stride + (x-bounds.Min.X)*4
	if idx < 0 || idx+3 >= len(img.Pix) {
		return color.RGBA{}
	}
	return color.RGBA{
		R: img.Pix[idx],
		G: img.Pix[idx+1],
		B: img.Pix[idx+2],
		A: img.Pix[idx+3],
	}
}

func bilinear(c00, c10, c01, c11, fx, fy float64) float64 {
	return c00*(1-fx)*(1-fy) + c10*fx*(1-fy) + c01*(1-fx)*fy + c11*fx*fy
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
