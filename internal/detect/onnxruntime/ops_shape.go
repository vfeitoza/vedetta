package onnxruntime

import (
	"fmt"
	"sort"
)

func init() {
	Register("Reshape", opReshape)
	Register("Transpose", opTranspose)
	Register("Concat", opConcat)
	Register("Slice", opSlice)
	Register("Split", opSplit)
	Register("Shape", opShape)
	Register("Gather", opGather)
	Register("Squeeze", opSqueeze)
	Register("Unsqueeze", opUnsqueeze)
	Register("Flatten", opFlatten)
	Register("Pad", opPad)
}

func opReshape(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("reshape: need 2 inputs, got %d", len(inputs))
	}
	data := inputs[0]
	shapeTensor := inputs[1]

	newShape := make([]int64, len(shapeTensor.Data))
	for i, v := range shapeTensor.Data {
		newShape[i] = int64(v)
	}

	// Handle 0 = copy from input shape
	for i, d := range newShape {
		if d == 0 && i < len(data.Shape) {
			newShape[i] = data.Shape[i]
		}
	}

	out, err := data.Reshape(newShape)
	if err != nil {
		return nil, fmt.Errorf("reshape: %w", err)
	}
	return []*Tensor{out}, nil
}

func opTranspose(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("transpose: need 1 input")
	}
	t := inputs[0]
	perm := attrs.GetIntList("perm")
	if perm == nil {
		// Default: reverse dimensions
		ndim := len(t.Shape)
		perm = make([]int64, ndim)
		for i := range perm {
			perm[i] = int64(ndim - 1 - i)
		}
	}
	return []*Tensor{t.Transpose(perm)}, nil
}

func opConcat(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("concat: need at least 1 input")
	}

	axis := int(attrs.GetInt("axis", 0))
	ndim := len(inputs[0].Shape)
	if axis < 0 {
		axis += ndim
	}
	if axis < 0 || axis >= ndim {
		return nil, fmt.Errorf("concat: axis %d out of range for %d dims", axis, ndim)
	}

	// Compute output shape
	outShape := make([]int64, ndim)
	copy(outShape, inputs[0].Shape)
	for _, inp := range inputs[1:] {
		for d := range ndim {
			if d == axis {
				outShape[d] += inp.Shape[d]
			} else if inp.Shape[d] != outShape[d] {
				return nil, fmt.Errorf("concat: shape mismatch on dim %d", d)
			}
		}
	}

	out := newTensorUninit(outShape)

	// Compute outerSize (product of dims before axis) and innerSize (product of dims after axis)
	outerSize := 1
	for d := 0; d < axis; d++ {
		outerSize *= int(outShape[d])
	}
	innerSize := 1
	for d := axis + 1; d < ndim; d++ {
		innerSize *= int(outShape[d])
	}

	// Total output axis size for stride computation
	outAxisSize := int(outShape[axis])

	// For each outer position, copy contiguous blocks from each input
	for outer := range outerSize {
		axisOffset := 0
		for _, inp := range inputs {
			inpAxisSize := int(inp.Shape[axis])
			blockSize := inpAxisSize * innerSize
			srcBase := outer * blockSize
			dstBase := outer*outAxisSize*innerSize + axisOffset*innerSize
			copy(out.Data[dstBase:dstBase+blockSize], inp.Data[srcBase:srcBase+blockSize])
			axisOffset += inpAxisSize
		}
	}

	return []*Tensor{out}, nil
}

func opSlice(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 3 {
		return nil, fmt.Errorf("slice: need at least 3 inputs")
	}
	data := inputs[0]
	ndim := len(data.Shape)

	starts := toInt64Slice(inputs[1].Data)
	ends := toInt64Slice(inputs[2].Data)
	nSlices := len(starts)

	axes := make([]int64, nSlices)
	if len(inputs) > 3 && inputs[3] != nil && len(inputs[3].Data) > 0 {
		axes = toInt64Slice(inputs[3].Data)
	} else {
		for i := range axes {
			axes[i] = int64(i)
		}
	}

	steps := make([]int64, nSlices)
	if len(inputs) > 4 && inputs[4] != nil && len(inputs[4].Data) > 0 {
		steps = toInt64Slice(inputs[4].Data)
	} else {
		for i := range steps {
			steps[i] = 1
		}
	}

	// Build per-dimension start/end/step (default: full range, step 1)
	dimStart := make([]int64, ndim)
	dimEnd := make([]int64, ndim)
	dimStep := make([]int64, ndim)
	for d := 0; d < ndim; d++ {
		dimStart[d] = 0
		dimEnd[d] = data.Shape[d]
		dimStep[d] = 1
	}

	for i := 0; i < nSlices; i++ {
		ax := int(axes[i])
		if ax < 0 {
			ax += ndim
		}
		s := starts[i]
		e := ends[i]
		st := steps[i]
		dimSize := data.Shape[ax]

		// Handle negative indices
		if s < 0 {
			s += dimSize
		}
		if e < 0 {
			e += dimSize
		}

		// Clamp
		if st > 0 {
			s = clampInt64(s, 0, dimSize)
			e = clampInt64(e, 0, dimSize)
		} else {
			s = clampInt64(s, 0, dimSize-1)
			e = clampInt64(e, -1, dimSize)
		}

		dimStart[ax] = s
		dimEnd[ax] = e
		dimStep[ax] = st
	}

	// Compute output shape
	outShape := make([]int64, ndim)
	for d := 0; d < ndim; d++ {
		if dimStep[d] > 0 {
			outShape[d] = (dimEnd[d] - dimStart[d] + dimStep[d] - 1) / dimStep[d]
		} else {
			outShape[d] = (dimStart[d] - dimEnd[d] - dimStep[d] - 1) / (-dimStep[d])
		}
		if outShape[d] < 0 {
			outShape[d] = 0
		}
	}

	out := NewTensor(outShape, nil)
	if out.Size() == 0 {
		return []*Tensor{out}, nil
	}

	inStrides := data.Strides()
	outStrides := out.Strides()
	outSize := out.Size()

	for i := 0; i < outSize; i++ {
		rem := int64(i)
		inIdx := int64(0)
		for d := 0; d < ndim; d++ {
			coord := rem / outStrides[d]
			rem %= outStrides[d]
			inCoord := dimStart[d] + coord*dimStep[d]
			inIdx += inCoord * inStrides[d]
		}
		out.Data[i] = data.Data[inIdx]
	}

	return []*Tensor{out}, nil
}

func opSplit(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("split: need at least 1 input")
	}
	data := inputs[0]
	ndim := len(data.Shape)

	axis := int(attrs.GetInt("axis", 0))
	if axis < 0 {
		axis += ndim
	}

	dimSize := data.Shape[axis]

	// Determine split sizes
	var splits []int64
	if len(inputs) > 1 && inputs[1] != nil && len(inputs[1].Data) > 0 {
		splits = toInt64Slice(inputs[1].Data)
	} else if sl := attrs.GetIntList("split"); sl != nil {
		splits = sl
	} else {
		// Equal split - not specified how many, default to splitting into individual slices
		// For ONNX opset >= 18, num_outputs is needed. We split equally.
		numOutputs := attrs.GetInt("num_outputs", dimSize)
		splits = make([]int64, numOutputs)
		base := dimSize / numOutputs
		remainder := dimSize % numOutputs
		for i := range splits {
			splits[i] = base
			if int64(i) < remainder {
				splits[i]++
			}
		}
	}

	results := make([]*Tensor, len(splits))

	// Compute outerSize (product of dims before axis) and innerSize (product of dims after axis)
	outerSize := 1
	for d := 0; d < axis; d++ {
		outerSize *= int(data.Shape[d])
	}
	innerSize := 1
	for d := axis + 1; d < ndim; d++ {
		innerSize *= int(data.Shape[d])
	}
	inAxisSize := int(dimSize)

	axisOffset := 0
	for si, splitSize := range splits {
		ss := int(splitSize)
		outShape := make([]int64, ndim)
		copy(outShape, data.Shape)
		outShape[axis] = splitSize

		out := NewTensor(outShape, nil)
		blockSize := ss * innerSize

		for outer := range outerSize {
			srcBase := outer*inAxisSize*innerSize + axisOffset*innerSize
			dstBase := outer * blockSize
			copy(out.Data[dstBase:dstBase+blockSize], data.Data[srcBase:srcBase+blockSize])
		}

		results[si] = out
		axisOffset += ss
	}

	return results, nil
}

func opShape(inputs []*Tensor, _ *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("shape: need 1 input")
	}
	shape := inputs[0].Shape
	data := make([]float32, len(shape))
	for i, d := range shape {
		data[i] = float32(d)
	}
	return []*Tensor{{Data: data, Shape: []int64{int64(len(shape))}}}, nil
}

func opGather(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("gather: need 2 inputs")
	}
	data := inputs[0]
	indices := inputs[1]
	axis := int(attrs.GetInt("axis", 0))
	ndim := len(data.Shape)
	if axis < 0 {
		axis += ndim
	}

	// Output shape: data.shape[:axis] + indices.shape + data.shape[axis+1:]
	outShape := make([]int64, 0, ndim-1+len(indices.Shape))
	outShape = append(outShape, data.Shape[:axis]...)
	outShape = append(outShape, indices.Shape...)
	outShape = append(outShape, data.Shape[axis+1:]...)

	// Handle scalar indices (0-dim) producing same rank as data
	if len(outShape) == 0 {
		outShape = []int64{}
	}

	out := NewTensor(outShape, nil)

	dataStrides := data.Strides()
	axisSize := data.Shape[axis]

	// Compute slice size after axis
	sliceSize := int64(1)
	for d := axis + 1; d < ndim; d++ {
		sliceSize *= data.Shape[d]
	}

	// Compute outer size before axis
	outerSize := int64(1)
	for d := 0; d < axis; d++ {
		outerSize *= data.Shape[d]
	}

	numIndices := int64(len(indices.Data))
	if numIndices == 0 {
		numIndices = 1 // scalar
	}

	outIdx := 0
	for outer := int64(0); outer < outerSize; outer++ {
		for ii := int64(0); ii < numIndices; ii++ {
			idx := int64(indices.Data[ii])
			if idx < 0 {
				idx += axisSize
			}
			srcBase := outer*dataStrides[axis]*axisSize + idx*dataStrides[axis]
			// dataStrides[axis] == sliceSize
			for s := int64(0); s < sliceSize; s++ {
				out.Data[outIdx] = data.Data[srcBase+s]
				outIdx++
			}
		}
	}

	return []*Tensor{out}, nil
}

func opSqueeze(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("squeeze: need at least 1 input")
	}
	data := inputs[0]
	ndim := len(data.Shape)

	var axes []int
	if len(inputs) > 1 && inputs[1] != nil && len(inputs[1].Data) > 0 {
		// Opset >= 13: axes as second input
		for _, v := range inputs[1].Data {
			ax := int(v)
			if ax < 0 {
				ax += ndim
			}
			axes = append(axes, ax)
		}
	} else if attrs != nil && len(attrs.IntLists["axes"]) > 0 {
		// Opset < 13: axes as attribute
		for _, v := range attrs.IntLists["axes"] {
			ax := int(v)
			if ax < 0 {
				ax += ndim
			}
			axes = append(axes, ax)
		}
	} else {
		// No axes specified: squeeze all dimensions of size 1
		for d := 0; d < ndim; d++ {
			if data.Shape[d] == 1 {
				axes = append(axes, d)
			}
		}
	}

	squeezeSet := make(map[int]bool, len(axes))
	for _, ax := range axes {
		squeezeSet[ax] = true
	}

	newShape := make([]int64, 0, ndim)
	for d := 0; d < ndim; d++ {
		if !squeezeSet[d] {
			newShape = append(newShape, data.Shape[d])
		}
	}

	return []*Tensor{{Data: data.Data, Shape: newShape}}, nil
}

func opUnsqueeze(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("unsqueeze: need at least 1 input")
	}
	data := inputs[0]

	// Opset >= 13: axes as second input tensor
	// Opset < 13: axes as attribute
	var axes []int
	if len(inputs) >= 2 {
		axesTensor := inputs[1]
		axes = make([]int, len(axesTensor.Data))
		for i, v := range axesTensor.Data {
			axes[i] = int(v)
		}
	} else if attrs != nil {
		axesAttr := attrs.IntLists["axes"]
		axes = make([]int, len(axesAttr))
		for i, v := range axesAttr {
			axes[i] = int(v)
		}
	} else {
		return nil, fmt.Errorf("unsqueeze: no axes provided (need 2 inputs or axes attribute)")
	}

	outRank := len(data.Shape) + len(axes)
	for i, ax := range axes {
		if ax < 0 {
			ax += outRank
		}
		axes[i] = ax
	}

	sort.Ints(axes)

	newShape := make([]int64, 0, outRank)
	// Copy data shape, inserting 1s at specified positions
	di := 0
	axIdx := 0
	for len(newShape) < outRank {
		if axIdx < len(axes) && len(newShape) == axes[axIdx] {
			newShape = append(newShape, 1)
			axIdx++
		} else {
			newShape = append(newShape, data.Shape[di])
			di++
		}
	}

	return []*Tensor{{Data: data.Data, Shape: newShape}}, nil
}

func opFlatten(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 1 {
		return nil, fmt.Errorf("flatten: need 1 input")
	}
	data := inputs[0]
	axis := int(attrs.GetInt("axis", 1))
	ndim := len(data.Shape)
	if axis < 0 {
		axis += ndim
	}

	// Compute [product(d0..d(axis-1)), product(d(axis)..d(n-1))]
	dim0 := int64(1)
	for d := 0; d < axis; d++ {
		dim0 *= data.Shape[d]
	}
	dim1 := int64(1)
	for d := axis; d < ndim; d++ {
		dim1 *= data.Shape[d]
	}

	return []*Tensor{{Data: data.Data, Shape: []int64{dim0, dim1}}}, nil
}

func opPad(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("pad: need at least 2 inputs")
	}
	data := inputs[0]
	padsTensor := inputs[1]
	ndim := len(data.Shape)

	constVal := float32(0)
	if len(inputs) > 2 && inputs[2] != nil && len(inputs[2].Data) > 0 {
		constVal = inputs[2].Data[0]
	}

	mode := attrs.GetString("mode", "constant")
	if mode != "constant" {
		return nil, fmt.Errorf("pad: only 'constant' mode supported, got %q", mode)
	}

	// pads format: [x1_begin, x2_begin, ..., xn_begin, x1_end, x2_end, ..., xn_end]
	pads := toInt64Slice(padsTensor.Data)
	if len(pads) != 2*ndim {
		return nil, fmt.Errorf("pad: pads length %d != 2*ndim %d", len(pads), 2*ndim)
	}

	outShape := make([]int64, ndim)
	for d := 0; d < ndim; d++ {
		outShape[d] = data.Shape[d] + pads[d] + pads[ndim+d]
	}

	out := NewTensor(outShape, nil)
	if constVal != 0 {
		out.Fill(constVal)
	}

	inStrides := data.Strides()
	outStrides := out.Strides()
	inSize := data.Size()

	for i := 0; i < inSize; i++ {
		rem := int64(i)
		outIdx := int64(0)
		for d := 0; d < ndim; d++ {
			coord := rem / inStrides[d]
			rem %= inStrides[d]
			outIdx += (coord + pads[d]) * outStrides[d]
		}
		out.Data[outIdx] = data.Data[i]
	}

	return []*Tensor{out}, nil
}

// toInt64Slice converts float32 data to int64 values.
func toInt64Slice(data []float32) []int64 {
	out := make([]int64, len(data))
	for i, v := range data {
		out[i] = int64(v)
	}
	return out
}

func clampInt64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
