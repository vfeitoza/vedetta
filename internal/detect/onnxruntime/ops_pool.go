package onnxruntime

import (
	"fmt"
	"math"
)

func init() {
	Register("MaxPool", opMaxPool)
	Register("GlobalAveragePool", opGlobalAveragePool)
	Register("AveragePool", opAveragePool)
}

func opMaxPool(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("MaxPool: need 1 input, got %d", len(inputs))
	}

	x := inputs[0]
	if x.Dims() != 4 {
		return nil, fmt.Errorf("MaxPool: expected 4D input, got %dD", x.Dims())
	}

	N := int(x.Shape[0])
	C := int(x.Shape[1])
	H := int(x.Shape[2])
	W := int(x.Shape[3])

	kernelShape := attrs.GetIntList("kernel_shape")
	if len(kernelShape) < 2 {
		return nil, fmt.Errorf("MaxPool: kernel_shape required")
	}
	kH := int(kernelShape[0])
	kW := int(kernelShape[1])

	strides := attrs.GetIntList("strides")
	strideH, strideW := 1, 1
	if len(strides) >= 2 {
		strideH = int(strides[0])
		strideW = int(strides[1])
	}

	pads := attrs.GetIntList("pads")
	padTop, padLeft, padBottom, padRight := 0, 0, 0, 0
	if len(pads) >= 4 {
		padTop = int(pads[0])
		padLeft = int(pads[1])
		padBottom = int(pads[2])
		padRight = int(pads[3])
	}

	dilations := attrs.GetIntList("dilations")
	dilH, dilW := 1, 1
	if len(dilations) >= 2 {
		dilH = int(dilations[0])
		dilW = int(dilations[1])
	}

	ceilMode := attrs.GetInt("ceil_mode", 0)

	effKH := (kH-1)*dilH + 1
	effKW := (kW-1)*dilW + 1

	var outH, outW int
	if ceilMode != 0 {
		outH = int(math.Ceil(float64(H+padTop+padBottom-effKH)/float64(strideH))) + 1
		outW = int(math.Ceil(float64(W+padLeft+padRight-effKW)/float64(strideW))) + 1
	} else {
		outH = (H+padTop+padBottom-effKH)/strideH + 1
		outW = (W+padLeft+padRight-effKW)/strideW + 1
	}

	output := NewTensor([]int64{int64(N), int64(C), int64(outH), int64(outW)}, nil)

	for n := 0; n < N; n++ {
		for c := 0; c < C; c++ {
			for oh := 0; oh < outH; oh++ {
				for ow := 0; ow < outW; ow++ {
					maxVal := float32(math.Inf(-1))
					for kh := 0; kh < kH; kh++ {
						ih := oh*strideH - padTop + kh*dilH
						for kw := 0; kw < kW; kw++ {
							iw := ow*strideW - padLeft + kw*dilW
							if ih >= 0 && ih < H && iw >= 0 && iw < W {
								v := x.Data[((n*C+c)*H+ih)*W+iw]
								if v > maxVal {
									maxVal = v
								}
							}
						}
					}
					output.Data[((n*C+c)*outH+oh)*outW+ow] = maxVal
				}
			}
		}
	}

	return []*Tensor{output}, nil
}

func opGlobalAveragePool(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("GlobalAveragePool: need 1 input, got %d", len(inputs))
	}

	x := inputs[0]
	if x.Dims() != 4 {
		return nil, fmt.Errorf("GlobalAveragePool: expected 4D input, got %dD", x.Dims())
	}

	N := int(x.Shape[0])
	C := int(x.Shape[1])
	H := int(x.Shape[2])
	W := int(x.Shape[3])
	spatial := H * W

	output := NewTensor([]int64{int64(N), int64(C), 1, 1}, nil)

	for n := 0; n < N; n++ {
		for c := 0; c < C; c++ {
			sum := float32(0)
			base := (n*C + c) * spatial
			for i := 0; i < spatial; i++ {
				sum += x.Data[base+i]
			}
			output.Data[n*C+c] = sum / float32(spatial)
		}
	}

	return []*Tensor{output}, nil
}

func opAveragePool(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("AveragePool: need 1 input, got %d", len(inputs))
	}

	x := inputs[0]
	if x.Dims() != 4 {
		return nil, fmt.Errorf("AveragePool: expected 4D input, got %dD", x.Dims())
	}

	N := int(x.Shape[0])
	C := int(x.Shape[1])
	H := int(x.Shape[2])
	W := int(x.Shape[3])

	kernelShape := attrs.GetIntList("kernel_shape")
	if len(kernelShape) < 2 {
		return nil, fmt.Errorf("AveragePool: kernel_shape required")
	}
	kH := int(kernelShape[0])
	kW := int(kernelShape[1])

	strides := attrs.GetIntList("strides")
	strideH, strideW := 1, 1
	if len(strides) >= 2 {
		strideH = int(strides[0])
		strideW = int(strides[1])
	}

	pads := attrs.GetIntList("pads")
	padTop, padLeft, padBottom, padRight := 0, 0, 0, 0
	if len(pads) >= 4 {
		padTop = int(pads[0])
		padLeft = int(pads[1])
		padBottom = int(pads[2])
		padRight = int(pads[3])
	}

	countIncludePad := attrs.GetInt("count_include_pad", 0)

	outH := (H+padTop+padBottom-kH)/strideH + 1
	outW := (W+padLeft+padRight-kW)/strideW + 1

	output := NewTensor([]int64{int64(N), int64(C), int64(outH), int64(outW)}, nil)

	for n := 0; n < N; n++ {
		for c := 0; c < C; c++ {
			for oh := 0; oh < outH; oh++ {
				for ow := 0; ow < outW; ow++ {
					sum := float32(0)
					count := 0
					for kh := 0; kh < kH; kh++ {
						ih := oh*strideH - padTop + kh
						for kw := 0; kw < kW; kw++ {
							iw := ow*strideW - padLeft + kw
							if ih >= 0 && ih < H && iw >= 0 && iw < W {
								sum += x.Data[((n*C+c)*H+ih)*W+iw]
								count++
							} else if countIncludePad != 0 {
								count++
							}
						}
					}
					if countIncludePad != 0 {
						count = kH * kW
					}
					if count > 0 {
						output.Data[((n*C+c)*outH+oh)*outW+ow] = sum / float32(count)
					}
				}
			}
		}
	}

	return []*Tensor{output}, nil
}
