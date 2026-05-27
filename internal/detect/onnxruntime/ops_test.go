package onnxruntime

import (
	"math"
	"testing"
)

const eps = 1e-5

func assertApprox(t *testing.T, got, want float32, msg string) {
	t.Helper()
	if float32(math.Abs(float64(got-want))) > eps {
		t.Errorf("%s: got %f, want %f", msg, got, want)
	}
}

// ======================================================================
// Conv tests
// ======================================================================

func TestConv1x1(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{1, 2, 3, 4})
	w := NewTensor([]int64{1, 1, 1, 1}, []float32{2})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 2, 2}, []float32{2, 4, 6, 8}, eps)
}

func TestConv3x3WithPadding(t *testing.T) {
	x := NewTensor([]int64{1, 1, 3, 3}, []float32{
		1, 2, 3,
		4, 5, 6,
		7, 8, 9,
	})
	w := NewTensor([]int64{1, 1, 3, 3}, []float32{
		0, 0, 0,
		0, 1, 0,
		0, 0, 0,
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{3, 3}
	attrs.IntLists["pads"] = []int64{1, 1, 1, 1}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 3, 3}, []float32{
		1, 2, 3,
		4, 5, 6,
		7, 8, 9,
	}, eps)
}

func TestConvWithBias(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{1, 2, 3, 4})
	w := NewTensor([]int64{1, 1, 1, 1}, []float32{1})
	b := NewTensor([]int64{1}, []float32{10})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}

	out, err := Execute("Conv", []*Tensor{x, w, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 2, 2}, []float32{11, 12, 13, 14}, eps)
}

func TestConvStride2(t *testing.T) {
	x := NewTensor([]int64{1, 1, 4, 4}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	})
	w := NewTensor([]int64{1, 1, 1, 1}, []float32{1})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}
	attrs.IntLists["strides"] = []int64{2, 2}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 2, 2}, []float32{1, 3, 9, 11}, eps)
}

func TestConvDepthwise(t *testing.T) {
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{
		1, 2, 3, 4, // channel 0
		5, 6, 7, 8, // channel 1
	})
	w := NewTensor([]int64{2, 1, 1, 1}, []float32{1, 2})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}
	attrs.Ints["group"] = 2

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 2, 2, 2}, []float32{
		1, 2, 3, 4, // channel 0 * 1
		10, 12, 14, 16, // channel 1 * 2
	}, eps)
}

func TestConvDilation(t *testing.T) {
	// 3x3 input, 2x2 kernel with dilation=2 → effective 3x3 kernel on 3x3 input → 1x1 output
	x := NewTensor([]int64{1, 1, 3, 3}, []float32{
		1, 0, 2,
		0, 0, 0,
		3, 0, 4,
	})
	w := NewTensor([]int64{1, 1, 2, 2}, []float32{1, 1, 1, 1})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["dilations"] = []int64{2, 2}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// Effective kernel hits: (0,0)=1, (0,2)=2, (2,0)=3, (2,2)=4 → sum=10
	assertTensorApprox(t, out[0], []int64{1, 1, 1, 1}, []float32{10}, eps)
}

func TestConvMultiChannel(t *testing.T) {
	// 2 input channels → 2 output channels (no groups)
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{
		1, 2, 3, 4, // ch0
		5, 6, 7, 8, // ch1
	})
	// 2 filters, each with 2 input channels, 1x1 kernel
	w := NewTensor([]int64{2, 2, 1, 1}, []float32{1, 0, 0, 1})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// Filter 0: ch0*1 + ch1*0 = [1,2,3,4]
	// Filter 1: ch0*0 + ch1*1 = [5,6,7,8]
	assertTensorApprox(t, out[0], []int64{1, 2, 2, 2}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
	}, eps)
}

func TestConvBatch2(t *testing.T) {
	x := NewTensor([]int64{2, 1, 2, 2}, []float32{
		1, 2, 3, 4, // batch 0
		5, 6, 7, 8, // batch 1
	})
	w := NewTensor([]int64{1, 1, 1, 1}, []float32{2})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 1, 2, 2}, []float32{
		2, 4, 6, 8,
		10, 12, 14, 16,
	}, eps)
}

func TestConvAutoPadSameUpper(t *testing.T) {
	x := NewTensor([]int64{1, 1, 3, 3}, []float32{
		1, 2, 3,
		4, 5, 6,
		7, 8, 9,
	})
	w := NewTensor([]int64{1, 1, 3, 3}, []float32{
		0, 0, 0,
		0, 1, 0,
		0, 0, 0,
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{3, 3}
	attrs.Strings["auto_pad"] = "SAME_UPPER"

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// SAME_UPPER preserves spatial dimensions
	if out[0].Shape[2] != 3 || out[0].Shape[3] != 3 {
		t.Fatalf("expected 3x3 output, got %v", out[0].Shape)
	}
	// Center kernel with SAME padding → output = input
	assertTensorApprox(t, out[0], []int64{1, 1, 3, 3}, []float32{
		1, 2, 3,
		4, 5, 6,
		7, 8, 9,
	}, eps)
}

func TestConv3x3SumKernel(t *testing.T) {
	// Verify a real convolution with all-1 kernel (sums the 3x3 window)
	x := NewTensor([]int64{1, 1, 3, 3}, []float32{
		1, 1, 1,
		1, 1, 1,
		1, 1, 1,
	})
	w := NewTensor([]int64{1, 1, 3, 3}, []float32{
		1, 1, 1,
		1, 1, 1,
		1, 1, 1,
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{3, 3}

	out, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// 3x3 all-1 kernel on 3x3 all-1 input without padding → 1x1 output = 9
	assertTensorApprox(t, out[0], []int64{1, 1, 1, 1}, []float32{9}, eps)
}

func TestConvErrorTooFewInputs(t *testing.T) {
	_, err := Execute("Conv", []*Tensor{NewTensor([]int64{1, 1, 2, 2}, nil)}, NewAttributes())
	if err == nil {
		t.Fatal("expected error for Conv with 1 input")
	}
}

func TestConvErrorNon4D(t *testing.T) {
	x := NewTensor([]int64{2, 3}, nil)
	w := NewTensor([]int64{2, 3}, nil)
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}
	_, err := Execute("Conv", []*Tensor{x, w}, attrs)
	if err == nil {
		t.Fatal("expected error for 2D Conv input")
	}
}

// ======================================================================
// Pooling tests
// ======================================================================

func TestMaxPool2x2(t *testing.T) {
	x := NewTensor([]int64{1, 1, 4, 4}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["strides"] = []int64{2, 2}

	out, err := Execute("MaxPool", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 2, 2}, []float32{6, 8, 14, 16}, eps)
}

func TestMaxPoolWithPadding(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{-1, -2, -3, -4})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{3, 3}
	attrs.IntLists["strides"] = []int64{1, 1}
	attrs.IntLists["pads"] = []int64{1, 1, 1, 1}

	out, err := Execute("MaxPool", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// With padding, the padded region is -inf, so max of visible values
	// Position (0,0) sees: [-inf,-inf,-inf, -inf,-1,-2, -inf,-3,-4] → max = -1
	assertApprox(t, out[0].Data[0], -1, "maxpool padded [0,0]")
}

func TestMaxPoolCeilMode(t *testing.T) {
	x := NewTensor([]int64{1, 1, 3, 3}, []float32{
		1, 2, 3,
		4, 5, 6,
		7, 8, 9,
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["strides"] = []int64{2, 2}
	attrs.Ints["ceil_mode"] = 1

	out, err := Execute("MaxPool", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// ceil_mode on 3x3 with k=2,s=2: ceil((3-2)/2)+1 = 2x2
	if out[0].Shape[2] != 2 || out[0].Shape[3] != 2 {
		t.Fatalf("expected 2x2 output with ceil_mode, got %v", out[0].Shape)
	}
}

func TestMaxPoolMultiChannel(t *testing.T) {
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{
		1, 2, 3, 4, // ch0
		10, 20, 30, 40, // ch1
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["strides"] = []int64{2, 2}

	out, err := Execute("MaxPool", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 2, 1, 1}, []float32{4, 40}, eps)
}

func TestGlobalAveragePool(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{1, 2, 3, 4})
	out, err := Execute("GlobalAveragePool", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 1, 1}, []float32{2.5}, eps)
}

func TestGlobalAveragePoolMultiChannel(t *testing.T) {
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{
		1, 3, 5, 7, // ch0: mean = 4
		2, 4, 6, 8, // ch1: mean = 5
	})
	out, err := Execute("GlobalAveragePool", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 2, 1, 1}, []float32{4, 5}, eps)
}

func TestAveragePool2x2(t *testing.T) {
	x := NewTensor([]int64{1, 1, 4, 4}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["strides"] = []int64{2, 2}

	out, err := Execute("AveragePool", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// (1+2+5+6)/4=3.5, (3+4+7+8)/4=5.5, (9+10+13+14)/4=11.5, (11+12+15+16)/4=13.5
	assertTensorApprox(t, out[0], []int64{1, 1, 2, 2}, []float32{3.5, 5.5, 11.5, 13.5}, eps)
}

func TestAveragePoolWithPadding(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{4, 4, 4, 4})
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["strides"] = []int64{1, 1}
	attrs.IntLists["pads"] = []int64{1, 1, 0, 0}

	out, err := Execute("AveragePool", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// With count_include_pad=0 (default), only count valid positions
	// Position (0,0): only (0,0) is valid → 4/1 = 4
	assertApprox(t, out[0].Data[0], 4, "avgpool padded no count_include")
}

// ======================================================================
// Math operation tests
// ======================================================================

func TestAddSameShape(t *testing.T) {
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{2, 3}, []float32{10, 20, 30, 40, 50, 60})
	out, err := Execute("Add", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 3}, []float32{11, 22, 33, 44, 55, 66}, eps)
}

func TestAddBroadcast(t *testing.T) {
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{3}, []float32{10, 20, 30})
	out, err := Execute("Add", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 3}, []float32{11, 22, 33, 14, 25, 36}, eps)
}

func TestAddScalarBroadcast(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	b := NewTensor([]int64{}, []float32{10})
	out, err := Execute("Add", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{11, 12, 13, 14}, eps)
}

func TestAddBroadcast4D(t *testing.T) {
	// [1,2,1,3] + [2,1,1] → broadcast to [1,2,1,3]
	a := NewTensor([]int64{1, 2, 1, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{2, 1, 1}, []float32{10, 20})
	out, err := Execute("Add", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// b broadcasts to [1,2,1,3] as [10,10,10, 20,20,20]
	assertTensorApprox(t, out[0], []int64{1, 2, 1, 3}, []float32{11, 12, 13, 24, 25, 26}, eps)
}

func TestMul(t *testing.T) {
	a := NewTensor([]int64{3}, []float32{2, 3, 4})
	b := NewTensor([]int64{3}, []float32{5, 6, 7})
	out, err := Execute("Mul", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3}, []float32{10, 18, 28}, eps)
}

func TestMulBroadcast(t *testing.T) {
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{1}, []float32{10})
	out, err := Execute("Mul", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 3}, []float32{10, 20, 30, 40, 50, 60}, eps)
}

func TestDiv(t *testing.T) {
	a := NewTensor([]int64{3}, []float32{10, 20, 30})
	b := NewTensor([]int64{3}, []float32{2, 4, 5})
	out, err := Execute("Div", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3}, []float32{5, 5, 6}, eps)
}

func TestDivBroadcast(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{10, 20, 30, 40})
	b := NewTensor([]int64{1, 2}, []float32{2, 5})
	out, err := Execute("Div", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{5, 4, 15, 8}, eps)
}

func TestSub(t *testing.T) {
	a := NewTensor([]int64{2}, []float32{10, 20})
	b := NewTensor([]int64{2}, []float32{3, 7})
	out, err := Execute("Sub", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{7, 13}, eps)
}

func TestSubBroadcast(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{10, 20, 30, 40})
	b := NewTensor([]int64{}, []float32{5})
	out, err := Execute("Sub", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{5, 15, 25, 35}, eps)
}

func TestMatMul2D(t *testing.T) {
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{3, 2}, []float32{1, 2, 3, 4, 5, 6})
	out, err := Execute("MatMul", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{22, 28, 49, 64}, eps)
}

func TestMatMulBatched(t *testing.T) {
	// [2, 2, 3] x [2, 3, 2] → [2, 2, 2]
	a := NewTensor([]int64{2, 2, 3}, []float32{
		1, 2, 3, 4, 5, 6, // batch 0
		7, 8, 9, 10, 11, 12, // batch 1
	})
	b := NewTensor([]int64{2, 3, 2}, []float32{
		1, 0, 0, 1, 1, 0, // batch 0
		1, 0, 0, 1, 1, 0, // batch 1
	})
	out, err := Execute("MatMul", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0].Shape) != 3 || out[0].Shape[0] != 2 || out[0].Shape[1] != 2 || out[0].Shape[2] != 2 {
		t.Fatalf("expected shape [2,2,2], got %v", out[0].Shape)
	}
	// Batch 0: [1,2,3; 4,5,6] x [1,0; 0,1; 1,0] = [4,2; 10,5]
	assertApprox(t, out[0].Data[0], 4, "batch0[0,0]")
	assertApprox(t, out[0].Data[1], 2, "batch0[0,1]")
	assertApprox(t, out[0].Data[2], 10, "batch0[1,0]")
	assertApprox(t, out[0].Data[3], 5, "batch0[1,1]")
}

func TestMatMulBroadcastBatch(t *testing.T) {
	// [2, 2, 3] x [3, 1] → [2, 2, 1] (broadcast b's batch)
	a := NewTensor([]int64{2, 2, 3}, []float32{
		1, 2, 3, 4, 5, 6, // batch 0
		7, 8, 9, 10, 11, 12, // batch 1
	})
	b := NewTensor([]int64{3, 1}, []float32{1, 1, 1})
	out, err := Execute("MatMul", []*Tensor{a, b}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// Each row sums: batch0=[6,15], batch1=[24,33]
	assertApprox(t, out[0].Data[0], 6, "b0r0")
	assertApprox(t, out[0].Data[1], 15, "b0r1")
	assertApprox(t, out[0].Data[2], 24, "b1r0")
	assertApprox(t, out[0].Data[3], 33, "b1r1")
}

func TestBinaryOpErrorTooFewInputs(t *testing.T) {
	_, err := Execute("Add", []*Tensor{NewTensor([]int64{2}, nil)}, NewAttributes())
	if err == nil {
		t.Fatal("expected error for Add with 1 input")
	}
}

func TestBinaryOpErrorIncompatibleShapes(t *testing.T) {
	a := NewTensor([]int64{3}, nil)
	b := NewTensor([]int64{4}, nil)
	_, err := Execute("Add", []*Tensor{a, b}, NewAttributes())
	if err == nil {
		t.Fatal("expected error for incompatible broadcast shapes [3] and [4]")
	}
}

// ======================================================================
// Activation tests
// ======================================================================

func TestSigmoid(t *testing.T) {
	x := NewTensor([]int64{3}, []float32{0, 100, -100})
	out, err := Execute("Sigmoid", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertApprox(t, out[0].Data[0], 0.5, "sigmoid(0)")
	if out[0].Data[1] < 0.99 {
		t.Errorf("sigmoid(100) should be ~1, got %f", out[0].Data[1])
	}
	if out[0].Data[2] > 0.01 {
		t.Errorf("sigmoid(-100) should be ~0, got %f", out[0].Data[2])
	}
}

func TestSigmoidKnownValues(t *testing.T) {
	x := NewTensor([]int64{4}, []float32{-1, 0, 1, 2})
	out, err := Execute("Sigmoid", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// sigmoid(-1) ≈ 0.2689, sigmoid(1) ≈ 0.7311, sigmoid(2) ≈ 0.8808
	assertApprox(t, out[0].Data[0], 0.26894, "sigmoid(-1)")
	assertApprox(t, out[0].Data[1], 0.5, "sigmoid(0)")
	assertApprox(t, out[0].Data[2], 0.73106, "sigmoid(1)")
	assertApprox(t, out[0].Data[3], 0.88080, "sigmoid(2)")
}

func TestSigmoidNaN(t *testing.T) {
	x := NewTensor([]int64{1}, []float32{float32(math.NaN())})
	out, err := Execute("Sigmoid", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(float64(out[0].Data[0])) {
		t.Errorf("sigmoid(NaN) should be NaN, got %f", out[0].Data[0])
	}
}

func TestRelu(t *testing.T) {
	x := NewTensor([]int64{5}, []float32{-2, -1, 0, 1, 2})
	out, err := Execute("Relu", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{5}, []float32{0, 0, 0, 1, 2}, eps)
}

func TestReluLargeNegative(t *testing.T) {
	x := NewTensor([]int64{3}, []float32{-1e10, 0, 1e10})
	out, err := Execute("Relu", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertApprox(t, out[0].Data[0], 0, "relu(-1e10)")
	assertApprox(t, out[0].Data[1], 0, "relu(0)")
	assertApprox(t, out[0].Data[2], 1e10, "relu(1e10)")
}

func TestSoftmax(t *testing.T) {
	x := NewTensor([]int64{1, 3}, []float32{1, 2, 3})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1
	out, err := Execute("Softmax", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	sum := out[0].Data[0] + out[0].Data[1] + out[0].Data[2]
	if !approxEqual(sum, 1.0, 1e-4) {
		t.Errorf("softmax sum = %f, want 1.0", sum)
	}
	if out[0].Data[0] >= out[0].Data[1] || out[0].Data[1] >= out[0].Data[2] {
		t.Errorf("softmax values should increase: %v", out[0].Data)
	}
}

func TestSoftmaxAxis0(t *testing.T) {
	// 2x2 tensor, softmax along axis 0
	x := NewTensor([]int64{2, 2}, []float32{0, 0, 0, 0})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Softmax", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// All equal → each should be 0.5 along axis 0
	for i := 0; i < 4; i++ {
		assertApprox(t, out[0].Data[i], 0.5, "softmax uniform")
	}
}

func TestSoftmaxNumericalStability(t *testing.T) {
	// Large values that would overflow naive exp() without max subtraction
	x := NewTensor([]int64{1, 3}, []float32{1000, 1001, 1002})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1
	out, err := Execute("Softmax", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	sum := out[0].Data[0] + out[0].Data[1] + out[0].Data[2]
	if !approxEqual(sum, 1.0, 1e-4) {
		t.Errorf("softmax sum with large values = %f, want 1.0", sum)
	}
	for _, v := range out[0].Data {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("softmax produced NaN/Inf: %v", out[0].Data)
		}
	}
}

func TestSoftmaxNegativeAxis(t *testing.T) {
	x := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	attrs := NewAttributes()
	attrs.Ints["axis"] = -1 // last axis
	out, err := Execute("Softmax", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// Each row should sum to 1
	sum0 := out[0].Data[0] + out[0].Data[1] + out[0].Data[2]
	sum1 := out[0].Data[3] + out[0].Data[4] + out[0].Data[5]
	if !approxEqual(sum0, 1.0, 1e-4) {
		t.Errorf("softmax row 0 sum = %f", sum0)
	}
	if !approxEqual(sum1, 1.0, 1e-4) {
		t.Errorf("softmax row 1 sum = %f", sum1)
	}
}

// ======================================================================
// BatchNorm tests
// ======================================================================

func TestBatchNorm(t *testing.T) {
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{1, 2, 3, 4, 5, 6, 7, 8})
	scale := NewTensor([]int64{2}, []float32{1, 1})
	bias := NewTensor([]int64{2}, []float32{0, 0})
	mean := NewTensor([]int64{2}, []float32{0, 0})
	variance := NewTensor([]int64{2}, []float32{1, 1})

	out, err := Execute("BatchNormalization", []*Tensor{x, scale, bias, mean, variance}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range x.Data {
		expected := v / float32(math.Sqrt(1+1e-5))
		assertApprox(t, out[0].Data[i], expected, "batchnorm identity")
	}
}

func TestBatchNormWithParams(t *testing.T) {
	x := NewTensor([]int64{1, 1, 1, 2}, []float32{4, 6})
	scale := NewTensor([]int64{1}, []float32{2})
	bias := NewTensor([]int64{1}, []float32{1})
	mean := NewTensor([]int64{1}, []float32{5})
	variance := NewTensor([]int64{1}, []float32{1})

	out, err := Execute("BatchNormalization", []*Tensor{x, scale, bias, mean, variance}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(out[0].Data[0], -1.0, 1e-4) {
		t.Errorf("batchnorm[0]: got %f, want -1.0", out[0].Data[0])
	}
	if !approxEqual(out[0].Data[1], 3.0, 1e-4) {
		t.Errorf("batchnorm[1]: got %f, want 3.0", out[0].Data[1])
	}
}

func TestBatchNormCustomEpsilon(t *testing.T) {
	x := NewTensor([]int64{1, 1, 1, 1}, []float32{2})
	scale := NewTensor([]int64{1}, []float32{1})
	bias := NewTensor([]int64{1}, []float32{0})
	mean := NewTensor([]int64{1}, []float32{0})
	variance := NewTensor([]int64{1}, []float32{3})
	attrs := NewAttributes()
	attrs.Floats["epsilon"] = 1.0 // large epsilon

	out, err := Execute("BatchNormalization", []*Tensor{x, scale, bias, mean, variance}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// (2-0)/sqrt(3+1)*1+0 = 2/2 = 1
	assertApprox(t, out[0].Data[0], 1.0, "batchnorm custom eps")
}

func TestBatchNormMultiBatch(t *testing.T) {
	// Batch=2, channels=1
	x := NewTensor([]int64{2, 1, 1, 1}, []float32{10, 20})
	scale := NewTensor([]int64{1}, []float32{1})
	bias := NewTensor([]int64{1}, []float32{0})
	mean := NewTensor([]int64{1}, []float32{10})
	variance := NewTensor([]int64{1}, []float32{1})

	out, err := Execute("BatchNormalization", []*Tensor{x, scale, bias, mean, variance}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// (10-10)/sqrt(1+eps) ≈ 0, (20-10)/sqrt(1+eps) ≈ 10
	assertApprox(t, out[0].Data[0], 0.0, "bn batch0")
	if !approxEqual(out[0].Data[1], 10.0, 1e-3) {
		t.Errorf("bn batch1: got %f, want ~10.0", out[0].Data[1])
	}
}

func TestBatchNormErrorTooFewInputs(t *testing.T) {
	x := NewTensor([]int64{1, 1, 1, 1}, nil)
	_, err := Execute("BatchNormalization", []*Tensor{x, x, x}, NewAttributes())
	if err == nil {
		t.Fatal("expected error for BatchNormalization with 3 inputs")
	}
}

// ======================================================================
// Shape operation tests
// ======================================================================

func TestReshape(t *testing.T) {
	x := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	shape := NewTensor([]int64{2}, []float32{3, 2})
	out, err := Execute("Reshape", []*Tensor{x, shape}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3, 2}, []float32{1, 2, 3, 4, 5, 6}, eps)
}

func TestReshapeInfer(t *testing.T) {
	x := NewTensor([]int64{2, 3, 4}, make([]float32, 24))
	shape := NewTensor([]int64{2}, []float32{2, -1})
	out, err := Execute("Reshape", []*Tensor{x, shape}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[0] != 2 || out[0].Shape[1] != 12 {
		t.Errorf("reshape with -1: got shape %v, want [2, 12]", out[0].Shape)
	}
}

func TestReshapeZeroDim(t *testing.T) {
	// 0 means "copy from input shape"
	x := NewTensor([]int64{3, 4, 5}, make([]float32, 60))
	shape := NewTensor([]int64{3}, []float32{0, -1, 5})
	out, err := Execute("Reshape", []*Tensor{x, shape}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// 0→3, -1→4, 5→5
	if out[0].Shape[0] != 3 || out[0].Shape[1] != 4 || out[0].Shape[2] != 5 {
		t.Errorf("reshape with 0 and -1: got %v, want [3,4,5]", out[0].Shape)
	}
}

func TestTranspose(t *testing.T) {
	x := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	attrs := NewAttributes()
	attrs.IntLists["perm"] = []int64{1, 0}
	out, err := Execute("Transpose", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3, 2}, []float32{1, 4, 2, 5, 3, 6}, eps)
}

func TestTransposeDefaultPerm(t *testing.T) {
	x := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	out, err := Execute("Transpose", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// Default perm = reverse = [1, 0] for 2D
	assertTensorApprox(t, out[0], []int64{3, 2}, []float32{1, 4, 2, 5, 3, 6}, eps)
}

func TestTranspose3D(t *testing.T) {
	// [2,3,1] → [1,2,3] perm = [2,0,1]
	x := NewTensor([]int64{2, 3, 1}, []float32{1, 2, 3, 4, 5, 6})
	attrs := NewAttributes()
	attrs.IntLists["perm"] = []int64{2, 0, 1}
	out, err := Execute("Transpose", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[0] != 1 || out[0].Shape[1] != 2 || out[0].Shape[2] != 3 {
		t.Fatalf("transpose 3D shape: got %v, want [1,2,3]", out[0].Shape)
	}
	assertTensorApprox(t, out[0], []int64{1, 2, 3}, []float32{1, 2, 3, 4, 5, 6}, eps)
}

func TestConcat(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	b := NewTensor([]int64{2, 2}, []float32{5, 6, 7, 8})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Concat", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{4, 2}, []float32{1, 2, 3, 4, 5, 6, 7, 8}, eps)
}

func TestConcatAxis1(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	b := NewTensor([]int64{2, 3}, []float32{5, 6, 7, 8, 9, 10})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1
	out, err := Execute("Concat", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 5}, []float32{1, 2, 5, 6, 7, 3, 4, 8, 9, 10}, eps)
}

func TestConcatNegativeAxis(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	b := NewTensor([]int64{2, 3}, []float32{5, 6, 7, 8, 9, 10})
	attrs := NewAttributes()
	attrs.Ints["axis"] = -1 // last axis
	out, err := Execute("Concat", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 5}, []float32{1, 2, 5, 6, 7, 3, 4, 8, 9, 10}, eps)
}

func TestConcatThreeTensors(t *testing.T) {
	a := NewTensor([]int64{1, 2}, []float32{1, 2})
	b := NewTensor([]int64{1, 2}, []float32{3, 4})
	c := NewTensor([]int64{1, 2}, []float32{5, 6})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Concat", []*Tensor{a, b, c}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3, 2}, []float32{1, 2, 3, 4, 5, 6}, eps)
}

func TestSlice(t *testing.T) {
	x := NewTensor([]int64{4}, []float32{10, 20, 30, 40})
	starts := NewTensor([]int64{1}, []float32{1})
	ends := NewTensor([]int64{1}, []float32{3})
	out, err := Execute("Slice", []*Tensor{x, starts, ends}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{20, 30}, eps)
}

func TestSliceNegativeIndices(t *testing.T) {
	x := NewTensor([]int64{5}, []float32{10, 20, 30, 40, 50})
	starts := NewTensor([]int64{1}, []float32{-3}) // index 2
	ends := NewTensor([]int64{1}, []float32{-1})   // index 4 (exclusive)
	out, err := Execute("Slice", []*Tensor{x, starts, ends}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{30, 40}, eps)
}

func TestSlice2D(t *testing.T) {
	x := NewTensor([]int64{3, 4}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
	})
	starts := NewTensor([]int64{2}, []float32{0, 1})
	ends := NewTensor([]int64{2}, []float32{2, 3})
	axes := NewTensor([]int64{2}, []float32{0, 1})
	out, err := Execute("Slice", []*Tensor{x, starts, ends, axes}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{2, 3, 6, 7}, eps)
}

func TestSliceWithStep(t *testing.T) {
	x := NewTensor([]int64{6}, []float32{10, 20, 30, 40, 50, 60})
	starts := NewTensor([]int64{1}, []float32{0})
	ends := NewTensor([]int64{1}, []float32{6})
	axes := NewTensor([]int64{1}, []float32{0})
	steps := NewTensor([]int64{1}, []float32{2})
	out, err := Execute("Slice", []*Tensor{x, starts, ends, axes, steps}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3}, []float32{10, 30, 50}, eps)
}

func TestSliceNegativeStep(t *testing.T) {
	x := NewTensor([]int64{4}, []float32{10, 20, 30, 40})
	starts := NewTensor([]int64{1}, []float32{3})
	ends := NewTensor([]int64{1}, []float32{0})
	axes := NewTensor([]int64{1}, []float32{0})
	steps := NewTensor([]int64{1}, []float32{-1})
	out, err := Execute("Slice", []*Tensor{x, starts, ends, axes, steps}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{3}, []float32{40, 30, 20}, eps)
}

func TestSplit(t *testing.T) {
	x := NewTensor([]int64{6}, []float32{1, 2, 3, 4, 5, 6})
	attrs := NewAttributes()
	attrs.IntLists["split"] = []int64{2, 2, 2}
	attrs.Ints["axis"] = 0
	out, err := Execute("Split", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("split: got %d outputs, want 3", len(out))
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{1, 2}, eps)
	assertTensorApprox(t, out[1], []int64{2}, []float32{3, 4}, eps)
	assertTensorApprox(t, out[2], []int64{2}, []float32{5, 6}, eps)
}

func TestSplitUnequalSizes(t *testing.T) {
	x := NewTensor([]int64{5}, []float32{1, 2, 3, 4, 5})
	attrs := NewAttributes()
	attrs.IntLists["split"] = []int64{2, 3}
	attrs.Ints["axis"] = 0
	out, err := Execute("Split", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("split: got %d outputs, want 2", len(out))
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{1, 2}, eps)
	assertTensorApprox(t, out[1], []int64{3}, []float32{3, 4, 5}, eps)
}

func TestSplitViaInputTensor(t *testing.T) {
	x := NewTensor([]int64{6}, []float32{1, 2, 3, 4, 5, 6})
	splitSizes := NewTensor([]int64{2}, []float32{4, 2})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Split", []*Tensor{x, splitSizes}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("split via tensor: got %d outputs, want 2", len(out))
	}
	assertTensorApprox(t, out[0], []int64{4}, []float32{1, 2, 3, 4}, eps)
	assertTensorApprox(t, out[1], []int64{2}, []float32{5, 6}, eps)
}

func TestSplit2D(t *testing.T) {
	x := NewTensor([]int64{2, 4}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
	})
	attrs := NewAttributes()
	attrs.IntLists["split"] = []int64{2, 2}
	attrs.Ints["axis"] = 1
	out, err := Execute("Split", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("split 2D: got %d outputs, want 2", len(out))
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{1, 2, 5, 6}, eps)
	assertTensorApprox(t, out[1], []int64{2, 2}, []float32{3, 4, 7, 8}, eps)
}

func TestShapeOp(t *testing.T) {
	x := NewTensor([]int64{2, 3, 4}, make([]float32, 24))
	out, err := Execute("Shape", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0].Data) != 3 {
		t.Fatalf("shape: got %d elements, want 3", len(out[0].Data))
	}
	assertApprox(t, out[0].Data[0], 2, "shape[0]")
	assertApprox(t, out[0].Data[1], 3, "shape[1]")
	assertApprox(t, out[0].Data[2], 4, "shape[2]")
}

func TestShapeScalar(t *testing.T) {
	x := ScalarTensor(42)
	out, err := Execute("Shape", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// Scalar has 0 dimensions, shape tensor should have 0 elements
	if len(out[0].Data) != 0 {
		t.Errorf("shape of scalar: got %d elements, want 0", len(out[0].Data))
	}
}

func TestConstant(t *testing.T) {
	attrs := NewAttributes()
	attrs.Tensors["value"] = NewTensor([]int64{2}, []float32{42, 43})
	out, err := Execute("Constant", nil, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{42, 43}, eps)
}

func TestConstantScalar(t *testing.T) {
	attrs := NewAttributes()
	attrs.Floats["value_float"] = 3.14
	out, err := Execute("Constant", nil, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertApprox(t, out[0].Data[0], 3.14, "constant scalar")
}

// ======================================================================
// Gather tests
// ======================================================================

func TestGather(t *testing.T) {
	x := NewTensor([]int64{3, 2}, []float32{1, 2, 3, 4, 5, 6})
	indices := NewTensor([]int64{2}, []float32{0, 2})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Gather", []*Tensor{x, indices}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{1, 2, 5, 6}, eps)
}

func TestGatherNegativeIndex(t *testing.T) {
	x := NewTensor([]int64{4}, []float32{10, 20, 30, 40})
	indices := NewTensor([]int64{2}, []float32{-1, -2})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Gather", []*Tensor{x, indices}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2}, []float32{40, 30}, eps)
}

func TestGatherAxis1(t *testing.T) {
	x := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	indices := NewTensor([]int64{2}, []float32{0, 2})
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1
	out, err := Execute("Gather", []*Tensor{x, indices}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// Row 0: cols 0,2 → [1,3], Row 1: cols 0,2 → [4,6]
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{1, 3, 4, 6}, eps)
}

// ======================================================================
// Squeeze / Unsqueeze tests
// ======================================================================

func TestUnsqueeze(t *testing.T) {
	x := NewTensor([]int64{3, 4}, make([]float32, 12))
	axes := NewTensor([]int64{2}, []float32{0, 2})
	out, err := Execute("Unsqueeze", []*Tensor{x, axes}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{1, 3, 1, 4}
	for i, d := range want {
		if out[0].Shape[i] != d {
			t.Errorf("shape[%d]: got %d, want %d", i, out[0].Shape[i], d)
		}
	}
}

func TestUnsqueezeNegativeAxis(t *testing.T) {
	x := NewTensor([]int64{3, 4}, make([]float32, 12))
	axes := NewTensor([]int64{1}, []float32{-1}) // last position
	out, err := Execute("Unsqueeze", []*Tensor{x, axes}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// [3,4] with axis -1 → [3,4,1]
	want := []int64{3, 4, 1}
	for i, d := range want {
		if out[0].Shape[i] != d {
			t.Errorf("shape[%d]: got %d, want %d", i, out[0].Shape[i], d)
		}
	}
}

func TestSqueeze(t *testing.T) {
	x := NewTensor([]int64{1, 3, 1, 4}, make([]float32, 12))
	axes := NewTensor([]int64{2}, []float32{0, 2})
	out, err := Execute("Squeeze", []*Tensor{x, axes}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{3, 4}
	if len(out[0].Shape) != len(want) {
		t.Fatalf("squeeze: got shape %v, want %v", out[0].Shape, want)
	}
	for i, d := range want {
		if out[0].Shape[i] != d {
			t.Errorf("squeeze shape[%d]: got %d, want %d", i, out[0].Shape[i], d)
		}
	}
}

func TestSqueezeAll(t *testing.T) {
	// No axes specified → squeeze all dims of size 1
	x := NewTensor([]int64{1, 3, 1, 1}, make([]float32, 3))
	out, err := Execute("Squeeze", []*Tensor{x}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0].Shape) != 1 || out[0].Shape[0] != 3 {
		t.Errorf("squeeze all: got shape %v, want [3]", out[0].Shape)
	}
}

// ======================================================================
// Flatten tests
// ======================================================================

func TestFlatten(t *testing.T) {
	x := NewTensor([]int64{2, 3, 4}, make([]float32, 24))
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1
	out, err := Execute("Flatten", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[0] != 2 || out[0].Shape[1] != 12 {
		t.Errorf("flatten axis=1: got shape %v, want [2,12]", out[0].Shape)
	}
}

func TestFlattenAxis0(t *testing.T) {
	x := NewTensor([]int64{2, 3, 4}, make([]float32, 24))
	attrs := NewAttributes()
	attrs.Ints["axis"] = 0
	out, err := Execute("Flatten", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[0] != 1 || out[0].Shape[1] != 24 {
		t.Errorf("flatten axis=0: got shape %v, want [1,24]", out[0].Shape)
	}
}

func TestFlattenAxisLast(t *testing.T) {
	x := NewTensor([]int64{2, 3, 4}, make([]float32, 24))
	attrs := NewAttributes()
	attrs.Ints["axis"] = -1
	out, err := Execute("Flatten", []*Tensor{x}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// axis=-1 → axis=2. [2,3,4] → [6, 4]
	if out[0].Shape[0] != 6 || out[0].Shape[1] != 4 {
		t.Errorf("flatten axis=-1: got shape %v, want [6,4]", out[0].Shape)
	}
}

// ======================================================================
// Pad tests
// ======================================================================

func TestPadConstant(t *testing.T) {
	x := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	// pads: [1,0, 0,1] → pad 1 on top, 1 on right
	pads := NewTensor([]int64{4}, []float32{1, 0, 0, 1})
	out, err := Execute("Pad", []*Tensor{x, pads}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	// shape: [2+1+0, 3+0+1] = [3, 4]
	want := []int64{3, 4}
	for i, d := range want {
		if out[0].Shape[i] != d {
			t.Errorf("pad shape[%d]: got %d, want %d", i, out[0].Shape[i], d)
		}
	}
	// First row should be zeros (padded)
	for j := 0; j < 4; j++ {
		assertApprox(t, out[0].Data[j], 0, "pad top row")
	}
	// Original data starts at row 1
	assertApprox(t, out[0].Data[4], 1, "pad data[1,0]")
	assertApprox(t, out[0].Data[5], 2, "pad data[1,1]")
	assertApprox(t, out[0].Data[6], 3, "pad data[1,2]")
	assertApprox(t, out[0].Data[7], 0, "pad right[1,3]")
}

func TestPadConstantValue(t *testing.T) {
	x := NewTensor([]int64{3}, []float32{1, 2, 3})
	pads := NewTensor([]int64{2}, []float32{1, 1}) // pad 1 on each side
	constVal := NewTensor([]int64{1}, []float32{-1})
	out, err := Execute("Pad", []*Tensor{x, pads, constVal}, NewAttributes())
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{5}, []float32{-1, 1, 2, 3, -1}, eps)
}

// ======================================================================
// Resize tests
// ======================================================================

func TestResizeNearest2x(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{1, 2, 3, 4})
	roi := NewTensor([]int64{0}, nil)
	scales := NewTensor([]int64{4}, []float32{1, 1, 2, 2})
	attrs := NewAttributes()
	attrs.Strings["mode"] = "nearest"
	out, err := Execute("Resize", []*Tensor{x, roi, scales}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	want := []float32{
		1, 1, 2, 2,
		1, 1, 2, 2,
		3, 3, 4, 4,
		3, 3, 4, 4,
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 4, 4}, want, eps)
}

func TestResizeNearestDownsample(t *testing.T) {
	x := NewTensor([]int64{1, 1, 4, 4}, []float32{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	})
	roi := NewTensor([]int64{0}, nil)
	scales := NewTensor([]int64{4}, []float32{1, 1, 0.5, 0.5})
	attrs := NewAttributes()
	attrs.Strings["mode"] = "nearest"
	out, err := Execute("Resize", []*Tensor{x, roi, scales}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 1, 2, 2}, []float32{1, 3, 9, 11}, eps)
}

func TestResizeBilinear(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{0, 10, 20, 30})
	roi := NewTensor([]int64{0}, nil)
	scales := NewTensor([]int64{4}, []float32{1, 1, 2, 2})
	attrs := NewAttributes()
	attrs.Strings["mode"] = "linear"
	out, err := Execute("Resize", []*Tensor{x, roi, scales}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[2] != 4 || out[0].Shape[3] != 4 {
		t.Fatalf("bilinear resize shape: got %v, want [1,1,4,4]", out[0].Shape)
	}
	// Just verify the output has reasonable interpolated values (no NaN/Inf)
	for i, v := range out[0].Data {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("bilinear output[%d] is NaN/Inf", i)
		}
	}
}

func TestResizeWithSizes(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, []float32{1, 2, 3, 4})
	roi := NewTensor([]int64{0}, nil)
	scales := NewTensor([]int64{0}, nil)                  // empty scales
	sizes := NewTensor([]int64{4}, []float32{1, 1, 4, 4}) // explicit output size
	attrs := NewAttributes()
	attrs.Strings["mode"] = "nearest"
	out, err := Execute("Resize", []*Tensor{x, roi, scales, sizes}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[2] != 4 || out[0].Shape[3] != 4 {
		t.Fatalf("resize with sizes: got shape %v, want [1,1,4,4]", out[0].Shape)
	}
}

func TestResizeMultiChannel(t *testing.T) {
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{
		1, 2, 3, 4, // ch 0
		10, 20, 30, 40, // ch 1
	})
	roi := NewTensor([]int64{0}, nil)
	scales := NewTensor([]int64{4}, []float32{1, 1, 2, 2})
	attrs := NewAttributes()
	attrs.Strings["mode"] = "nearest"
	out, err := Execute("Resize", []*Tensor{x, roi, scales}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Shape[1] != 2 || out[0].Shape[2] != 4 || out[0].Shape[3] != 4 {
		t.Fatalf("resize multi-ch: got shape %v", out[0].Shape)
	}
	// Check ch1 corner values
	ch1Start := 4 * 4 // after ch0's 4x4 = 16 elements
	assertApprox(t, out[0].Data[ch1Start], 10, "resize ch1[0,0]")
	assertApprox(t, out[0].Data[ch1Start+3], 20, "resize ch1[0,3]")
}

// ======================================================================
// Error path tests
// ======================================================================

func TestUnknownOp(t *testing.T) {
	_, err := Execute("NonExistentOp", nil, NewAttributes())
	if err == nil {
		t.Fatal("expected error for unknown op")
	}
}

func TestMaxPoolMissingKernelShape(t *testing.T) {
	x := NewTensor([]int64{1, 1, 4, 4}, nil)
	_, err := Execute("MaxPool", []*Tensor{x}, NewAttributes())
	if err == nil {
		t.Fatal("expected error for MaxPool without kernel_shape")
	}
}

func TestReshapeIncompatible(t *testing.T) {
	x := NewTensor([]int64{2, 3}, make([]float32, 6))
	shape := NewTensor([]int64{2}, []float32{2, 2}) // 4 != 6
	_, err := Execute("Reshape", []*Tensor{x, shape}, NewAttributes())
	if err == nil {
		t.Fatal("expected error for incompatible reshape")
	}
}

func TestConcatShapeMismatch(t *testing.T) {
	a := NewTensor([]int64{2, 3}, nil)
	b := NewTensor([]int64{3, 3}, nil) // dim0 differs when concat on axis 1
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1
	_, err := Execute("Concat", []*Tensor{a, b}, attrs)
	if err == nil {
		t.Fatal("expected error for Concat shape mismatch")
	}
}

func TestResizeUnsupportedMode(t *testing.T) {
	x := NewTensor([]int64{1, 1, 2, 2}, nil)
	roi := NewTensor([]int64{0}, nil)
	scales := NewTensor([]int64{4}, []float32{1, 1, 2, 2})
	attrs := NewAttributes()
	attrs.Strings["mode"] = "cubic"
	_, err := Execute("Resize", []*Tensor{x, roi, scales}, attrs)
	if err == nil {
		t.Fatal("expected error for unsupported resize mode")
	}
}

func TestSoftmaxInvalidAxis(t *testing.T) {
	x := NewTensor([]int64{2, 3}, nil)
	attrs := NewAttributes()
	attrs.Ints["axis"] = 5
	_, err := Execute("Softmax", []*Tensor{x}, attrs)
	if err == nil {
		t.Fatal("expected error for out-of-range softmax axis")
	}
}

// ======================================================================
// PRelu tests
// ======================================================================

func TestPReluBasic(t *testing.T) {
	// x has positive and negative values, slope is per-element (same shape)
	x := NewTensor([]int64{2, 3}, []float32{1, -2, 3, -4, 5, -6})
	slope := NewTensor([]int64{2, 3}, []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6})
	attrs := NewAttributes()

	out, err := Execute("PRelu", []*Tensor{x, slope}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// positive: identity, negative: slope * x
	assertTensorApprox(t, out[0], []int64{2, 3}, []float32{
		1, -0.4, 3, -1.6, 5, -3.6,
	}, eps)
}

func TestPReluAllPositive(t *testing.T) {
	x := NewTensor([]int64{4}, []float32{1, 2, 3, 4})
	slope := NewTensor([]int64{4}, []float32{0.5, 0.5, 0.5, 0.5})
	attrs := NewAttributes()

	out, err := Execute("PRelu", []*Tensor{x, slope}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// All positive: output equals input
	assertTensorApprox(t, out[0], []int64{4}, []float32{1, 2, 3, 4}, eps)
}

func TestPReluAllNegative(t *testing.T) {
	x := NewTensor([]int64{4}, []float32{-1, -2, -3, -4})
	slope := NewTensor([]int64{4}, []float32{0.25, 0.25, 0.25, 0.25})
	attrs := NewAttributes()

	out, err := Execute("PRelu", []*Tensor{x, slope}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{4}, []float32{-0.25, -0.5, -0.75, -1.0}, eps)
}

func TestPReluScalarSlope(t *testing.T) {
	x := NewTensor([]int64{2, 2}, []float32{1, -2, -3, 4})
	slope := NewTensor([]int64{1}, []float32{0.1})
	attrs := NewAttributes()

	out, err := Execute("PRelu", []*Tensor{x, slope}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{1, -0.2, -0.3, 4}, eps)
}

func TestPReluPerChannel(t *testing.T) {
	// [1, 2, 2, 2] with slope [2, 1, 1] (per-channel)
	x := NewTensor([]int64{1, 2, 2, 2}, []float32{
		1, -1, 2, -2, // channel 0
		-3, 3, -4, 4, // channel 1
	})
	slope := NewTensor([]int64{2, 1, 1}, []float32{0.1, 0.2})
	attrs := NewAttributes()

	out, err := Execute("PRelu", []*Tensor{x, slope}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 2, 2, 2}, []float32{
		1, -0.1, 2, -0.2, // channel 0: slope=0.1
		-0.6, 3, -0.8, 4, // channel 1: slope=0.2
	}, eps)
}

func TestPReluPerChannel1C11(t *testing.T) {
	// slope shape [1,C,1,1]
	x := NewTensor([]int64{1, 3, 1, 1}, []float32{-1, 2, -3})
	slope := NewTensor([]int64{1, 3, 1, 1}, []float32{0.1, 0.2, 0.3})
	attrs := NewAttributes()

	out, err := Execute("PRelu", []*Tensor{x, slope}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{1, 3, 1, 1}, []float32{-0.1, 2, -0.9}, eps)
}

func TestPReluInsufficientInputs(t *testing.T) {
	x := NewTensor([]int64{2}, []float32{1, 2})
	attrs := NewAttributes()
	_, err := Execute("PRelu", []*Tensor{x}, attrs)
	if err == nil {
		t.Fatal("expected error for insufficient inputs")
	}
}

// ======================================================================
// Gemm tests
// ======================================================================

func TestGemmBasicNoTranspose(t *testing.T) {
	// A [2,3] * B [3,2] = C [2,2], alpha=1, beta=0, no bias
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{3, 2}, []float32{7, 8, 9, 10, 11, 12})
	attrs := NewAttributes()

	out, err := Execute("Gemm", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// [1*7+2*9+3*11, 1*8+2*10+3*12] = [58, 64]
	// [4*7+5*9+6*11, 4*8+5*10+6*12] = [139, 154]
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{58, 64, 139, 154}, eps)
}

func TestGemmTransB(t *testing.T) {
	// A [2,3] * B^T where B is [2,3], transB=1 → B' is [3,2]
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{2, 3}, []float32{7, 9, 11, 8, 10, 12})
	attrs := NewAttributes()
	attrs.Ints["transB"] = 1

	out, err := Execute("Gemm", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// B^T = [[7,8],[9,10],[11,12]], same result as basic test
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{58, 64, 139, 154}, eps)
}

func TestGemmTransA(t *testing.T) {
	// A^T where A is [3,2], transA=1 → A' is [2,3], then * B [3,2]
	a := NewTensor([]int64{3, 2}, []float32{1, 4, 2, 5, 3, 6})
	b := NewTensor([]int64{3, 2}, []float32{7, 8, 9, 10, 11, 12})
	attrs := NewAttributes()
	attrs.Ints["transA"] = 1

	out, err := Execute("Gemm", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// A^T = [[1,2,3],[4,5,6]], same result as basic test
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{58, 64, 139, 154}, eps)
}

func TestGemmWithBias1D(t *testing.T) {
	// A [2,3] * B [3,2] + C [2] (1D bias broadcast per row)
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{3, 2}, []float32{7, 8, 9, 10, 11, 12})
	c := NewTensor([]int64{2}, []float32{100, 200})
	attrs := NewAttributes()

	out, err := Execute("Gemm", []*Tensor{a, b, c}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{158, 264, 239, 354}, eps)
}

func TestGemmWithBias2D(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{1, 0, 0, 1})
	b := NewTensor([]int64{2, 2}, []float32{5, 6, 7, 8})
	c := NewTensor([]int64{2, 2}, []float32{10, 20, 30, 40})
	attrs := NewAttributes()

	out, err := Execute("Gemm", []*Tensor{a, b, c}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// Identity * B + C = B + C
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{15, 26, 37, 48}, eps)
}

func TestGemmAlphaBeta(t *testing.T) {
	// Y = 2.0 * A * B + 0.5 * C
	a := NewTensor([]int64{1, 2}, []float32{1, 2})
	b := NewTensor([]int64{2, 1}, []float32{3, 4})
	c := NewTensor([]int64{1}, []float32{10})
	attrs := NewAttributes()
	attrs.Floats["alpha"] = 2.0
	attrs.Floats["beta"] = 0.5

	out, err := Execute("Gemm", []*Tensor{a, b, c}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// A*B = [1*3+2*4] = [11], alpha*11 = 22, beta*10 = 5, total = 27
	assertTensorApprox(t, out[0], []int64{1, 1}, []float32{27}, eps)
}

func TestGemmNonSquare(t *testing.T) {
	// A [1,4] * B [4,2] = C [1,2]
	a := NewTensor([]int64{1, 4}, []float32{1, 2, 3, 4})
	b := NewTensor([]int64{4, 2}, []float32{1, 2, 3, 4, 5, 6, 7, 8})
	attrs := NewAttributes()

	out, err := Execute("Gemm", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// [1*1+2*3+3*5+4*7, 1*2+2*4+3*6+4*8] = [50, 60]
	assertTensorApprox(t, out[0], []int64{1, 2}, []float32{50, 60}, eps)
}

func TestGemmMobileFaceNetCase(t *testing.T) {
	// Simulates the MobileFaceNet pattern: transB=1, weight [N,K], bias [N]
	// A [1,4] * B^T where B is [3,4] → result [1,3] + bias [3]
	a := NewTensor([]int64{1, 4}, []float32{1, 2, 3, 4})
	b := NewTensor([]int64{3, 4}, []float32{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
	})
	c := NewTensor([]int64{3}, []float32{10, 20, 30})
	attrs := NewAttributes()
	attrs.Ints["transB"] = 1

	out, err := Execute("Gemm", []*Tensor{a, b, c}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// B^T selects first 3 elements of A: [1, 2, 3] + bias = [11, 22, 33]
	assertTensorApprox(t, out[0], []int64{1, 3}, []float32{11, 22, 33}, eps)
}

func TestGemmDimensionMismatch(t *testing.T) {
	a := NewTensor([]int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	b := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	attrs := NewAttributes()

	_, err := Execute("Gemm", []*Tensor{a, b}, attrs)
	if err == nil {
		t.Fatal("expected error for inner dimension mismatch")
	}
}

func TestGemmInsufficientInputs(t *testing.T) {
	a := NewTensor([]int64{2, 2}, []float32{1, 2, 3, 4})
	attrs := NewAttributes()
	_, err := Execute("Gemm", []*Tensor{a}, attrs)
	if err == nil {
		t.Fatal("expected error for insufficient inputs")
	}
}

func TestGemmNoBiasNonUnitAlpha(t *testing.T) {
	// alpha=0.5, no bias
	a := NewTensor([]int64{2, 2}, []float32{2, 0, 0, 2})
	b := NewTensor([]int64{2, 2}, []float32{3, 0, 0, 3})
	attrs := NewAttributes()
	attrs.Floats["alpha"] = 0.5

	out, err := Execute("Gemm", []*Tensor{a, b}, attrs)
	if err != nil {
		t.Fatal(err)
	}
	// A*B = [[6,0],[0,6]], * 0.5 = [[3,0],[0,3]]
	assertTensorApprox(t, out[0], []int64{2, 2}, []float32{3, 0, 0, 3}, eps)
}
