package onnxruntime

import (
	"fmt"
	"math"
)

func init() {
	Register("Conv", opConv)
}

func opConv(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("Conv: need at least 2 inputs (X, W), got %d", len(inputs))
	}

	x := inputs[0] // [N, C, H, W]
	w := inputs[1] // [M, C/group, kH, kW]

	if x.Dims() != 4 || w.Dims() != 4 {
		return nil, fmt.Errorf("Conv: expected 4D inputs, got X=%dD W=%dD", x.Dims(), w.Dims())
	}

	var bias []float32
	if len(inputs) > 2 && inputs[2] != nil {
		bias = inputs[2].Data
	}

	N := int(x.Shape[0])
	C := int(x.Shape[1])
	H := int(x.Shape[2])
	W := int(x.Shape[3])

	M := int(w.Shape[0])
	kH := int(w.Shape[2])
	kW := int(w.Shape[3])

	group := int(attrs.GetInt("group", 1))

	kernelShape := attrs.GetIntList("kernel_shape")
	if kernelShape != nil {
		kH = int(kernelShape[0])
		kW = int(kernelShape[1])
	}

	strides := attrs.GetIntList("strides")
	strideH, strideW := 1, 1
	if len(strides) >= 2 {
		strideH = int(strides[0])
		strideW = int(strides[1])
	}

	dilations := attrs.GetIntList("dilations")
	dilH, dilW := 1, 1
	if len(dilations) >= 2 {
		dilH = int(dilations[0])
		dilW = int(dilations[1])
	}

	pads := attrs.GetIntList("pads")
	padTop, padLeft, padBottom, padRight := 0, 0, 0, 0
	if len(pads) >= 4 {
		padTop = int(pads[0])
		padLeft = int(pads[1])
		padBottom = int(pads[2])
		padRight = int(pads[3])
	}

	// Handle auto_pad attribute
	autoPad := attrs.GetString("auto_pad", "NOTSET")
	if autoPad == "SAME_UPPER" || autoPad == "SAME_LOWER" {
		outH := int(math.Ceil(float64(H) / float64(strideH)))
		outW := int(math.Ceil(float64(W) / float64(strideW)))
		totalPadH := (outH-1)*strideH + (kH-1)*dilH + 1 - H
		totalPadW := (outW-1)*strideW + (kW-1)*dilW + 1 - W
		if totalPadH < 0 {
			totalPadH = 0
		}
		if totalPadW < 0 {
			totalPadW = 0
		}
		if autoPad == "SAME_UPPER" {
			padTop = totalPadH / 2
			padBottom = totalPadH - padTop
			padLeft = totalPadW / 2
			padRight = totalPadW - padLeft
		} else {
			padBottom = totalPadH / 2
			padTop = totalPadH - padBottom
			padRight = totalPadW / 2
			padLeft = totalPadW - padRight
		}
	}

	effKH := (kH-1)*dilH + 1
	effKW := (kW-1)*dilW + 1
	outH := (H + padTop + padBottom - effKH) / strideH + 1
	outW := (W + padLeft + padRight - effKW) / strideW + 1

	if outH <= 0 || outW <= 0 {
		return nil, fmt.Errorf("Conv: invalid output dimensions %dx%d", outH, outW)
	}

	cPerGroup := C / group
	mPerGroup := M / group
	colSize := cPerGroup * kH * kW
	outSpatial := outH * outW

	output := NewTensor([]int64{int64(N), int64(M), int64(outH), int64(outW)}, nil)

	col := make([]float32, colSize*outSpatial)

	for n := 0; n < N; n++ {
		for g := 0; g < group; g++ {
			// im2col for this batch and group
			im2col(
				x.Data, col,
				n, g*cPerGroup, cPerGroup,
				H, W, kH, kW,
				strideH, strideW, padTop, padLeft,
				dilH, dilW, outH, outW,
				C,
			)

			// Extract weight slice for this group: [mPerGroup, cPerGroup*kH*kW]
			wOffset := g * mPerGroup * colSize
			wSlice := w.Data[wOffset : wOffset+mPerGroup*colSize]

			// GEMM: [mPerGroup, colSize] x [colSize, outSpatial] = [mPerGroup, outSpatial]
			result := Sgemm(wSlice, col, mPerGroup, outSpatial, colSize)

			// Copy result into output tensor
			for m := 0; m < mPerGroup; m++ {
				outChannel := g*mPerGroup + m
				dstBase := ((n*M + outChannel) * outH) * outW
				srcBase := m * outSpatial
				copy(output.Data[dstBase:dstBase+outSpatial], result[srcBase:srcBase+outSpatial])
			}
		}
	}

	// Add bias
	if bias != nil {
		for n := 0; n < N; n++ {
			for m := 0; m < M; m++ {
				base := ((n*M + m) * outH) * outW
				b := bias[m]
				for i := 0; i < outSpatial; i++ {
					output.Data[base+i] += b
				}
			}
		}
	}

	return []*Tensor{output}, nil
}

// im2col converts input patches into columns for GEMM-based convolution.
func im2col(
	input, col []float32,
	n, cStart, cCount int,
	H, W, kH, kW int,
	strideH, strideW, padTop, padLeft int,
	dilH, dilW, outH, outW int,
	totalC int,
) {
	colIdx := 0
	for c := 0; c < cCount; c++ {
		ch := cStart + c
		for kh := 0; kh < kH; kh++ {
			for kw := 0; kw < kW; kw++ {
				for oh := 0; oh < outH; oh++ {
					ih := oh*strideH - padTop + kh*dilH
					for ow := 0; ow < outW; ow++ {
						iw := ow*strideW - padLeft + kw*dilW
						if ih >= 0 && ih < H && iw >= 0 && iw < W {
							col[colIdx] = input[((n*totalC+ch)*H+ih)*W+iw]
						} else {
							col[colIdx] = 0
						}
						colIdx++
					}
				}
			}
		}
	}
}
