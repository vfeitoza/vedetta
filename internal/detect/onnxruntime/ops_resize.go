package onnxruntime

import (
	"fmt"
	"math"
)

func init() {
	Register("Resize", opResize)
}

func opResize(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("resize: need at least 1 input")
	}
	x := inputs[0]
	if len(x.Shape) != 4 {
		return nil, fmt.Errorf("resize: expected 4D input [N,C,H,W], got %dD", len(x.Shape))
	}

	n := x.Shape[0]
	c := x.Shape[1]
	inH := x.Shape[2]
	inW := x.Shape[3]

	var outH, outW int64

	// Determine output size from scales (input[2]) or sizes (input[3])
	if len(inputs) > 3 && inputs[3] != nil && len(inputs[3].Data) == 4 {
		// sizes tensor: [N, C, outH, outW]
		outH = int64(inputs[3].Data[2])
		outW = int64(inputs[3].Data[3])
	} else if len(inputs) > 2 && inputs[2] != nil && len(inputs[2].Data) == 4 {
		// scales tensor: [scaleN, scaleC, scaleH, scaleW]
		outH = int64(float32(inH) * inputs[2].Data[2])
		outW = int64(float32(inW) * inputs[2].Data[3])
	} else {
		return nil, fmt.Errorf("resize: need either scales or sizes input")
	}

	if outH <= 0 || outW <= 0 {
		return nil, fmt.Errorf("resize: invalid output size %dx%d", outH, outW)
	}

	mode := attrs.GetString("mode", "nearest")

	outShape := []int64{n, c, outH, outW}
	out := NewTensor(outShape, nil)

	switch mode {
	case "nearest":
		resizeNearest(x, out, n, c, inH, inW, outH, outW)
	case "linear":
		resizeBilinear(x, out, n, c, inH, inW, outH, outW)
	default:
		return nil, fmt.Errorf("resize: unsupported mode %q", mode)
	}

	return []*Tensor{out}, nil
}

func resizeNearest(x, out *Tensor, n, c, inH, inW, outH, outW int64) {
	for ni := int64(0); ni < n; ni++ {
		for ci := int64(0); ci < c; ci++ {
			inBase := (ni*c + ci) * inH * inW
			outBase := (ni*c + ci) * outH * outW
			for y := int64(0); y < outH; y++ {
				srcY := y * inH / outH
				if srcY >= inH {
					srcY = inH - 1
				}
				for xi := int64(0); xi < outW; xi++ {
					srcX := xi * inW / outW
					if srcX >= inW {
						srcX = inW - 1
					}
					out.Data[outBase+y*outW+xi] = x.Data[inBase+srcY*inW+srcX]
				}
			}
		}
	}
}

func resizeBilinear(x, out *Tensor, n, c, inH, inW, outH, outW int64) {
	scaleH := float64(inH) / float64(outH)
	scaleW := float64(inW) / float64(outW)

	for ni := int64(0); ni < n; ni++ {
		for ci := int64(0); ci < c; ci++ {
			inBase := (ni*c + ci) * inH * inW
			outBase := (ni*c + ci) * outH * outW
			for y := int64(0); y < outH; y++ {
				inY := (float64(y)+0.5)*scaleH - 0.5
				y0 := int64(math.Floor(inY))
				y1 := y0 + 1
				fy := float32(inY - float64(y0))

				if y0 < 0 {
					y0 = 0
				}
				if y1 >= inH {
					y1 = inH - 1
				}

				for xi := int64(0); xi < outW; xi++ {
					inX := (float64(xi)+0.5)*scaleW - 0.5
					x0 := int64(math.Floor(inX))
					x1 := x0 + 1
					fx := float32(inX - float64(x0))

					if x0 < 0 {
						x0 = 0
					}
					if x1 >= inW {
						x1 = inW - 1
					}

					v00 := x.Data[inBase+y0*inW+x0]
					v01 := x.Data[inBase+y0*inW+x1]
					v10 := x.Data[inBase+y1*inW+x0]
					v11 := x.Data[inBase+y1*inW+x1]

					v := v00*(1-fx)*(1-fy) + v01*fx*(1-fy) + v10*(1-fx)*fy + v11*fx*fy
					out.Data[outBase+y*outW+xi] = v
				}
			}
		}
	}
}
