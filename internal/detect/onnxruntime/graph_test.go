package onnxruntime

import (
	"encoding/binary"
	"math"
	"testing"
)

// ======================================================================
// Protobuf encoder helpers (test-only) for building synthetic ONNX models
// ======================================================================

func protoVarint(v uint64) []byte {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	return buf[:n+1]
}

func protoTag(fieldNum, wireType int) []byte {
	return protoVarint(uint64(fieldNum<<3 | wireType))
}

func protoBytes(fieldNum int, data []byte) []byte {
	var out []byte
	out = append(out, protoTag(fieldNum, wireBytes)...)
	out = append(out, protoVarint(uint64(len(data)))...)
	out = append(out, data...)
	return out
}

func protoString(fieldNum int, s string) []byte {
	return protoBytes(fieldNum, []byte(s))
}

func protoVarintField(fieldNum int, v uint64) []byte {
	var out []byte
	out = append(out, protoTag(fieldNum, wireVarint)...)
	out = append(out, protoVarint(v)...)
	return out
}

func protoFixed32Field(fieldNum int, v uint32) []byte {
	var out []byte
	out = append(out, protoTag(fieldNum, wire32Bit)...)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	out = append(out, buf[:]...)
	return out
}

func encodePackedFloat32(vals []float32) []byte {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func encodePackedInt64(vals []int64) []byte {
	var buf []byte
	for _, v := range vals {
		buf = append(buf, protoVarint(uint64(v))...)
	}
	return buf
}

// buildTensorProto builds a serialized TensorProto with float data.
func buildTensorProto(name string, dims []int64, data []float32) []byte {
	var tp []byte
	// field 1: dims (packed int64)
	if len(dims) > 0 {
		tp = append(tp, protoBytes(1, encodePackedInt64(dims))...)
	}
	// field 2: data_type = FLOAT (1)
	tp = append(tp, protoVarintField(2, 1)...)
	// field 4: float_data (packed float32)
	if len(data) > 0 {
		tp = append(tp, protoBytes(4, encodePackedFloat32(data))...)
	}
	// field 8: name
	if name != "" {
		tp = append(tp, protoString(8, name)...)
	}
	return tp
}

// buildTensorProtoRawData builds a serialized TensorProto with raw_data.
func buildTensorProtoRawData(name string, dims []int64, dataType int, rawData []byte) []byte {
	var tp []byte
	if len(dims) > 0 {
		tp = append(tp, protoBytes(1, encodePackedInt64(dims))...)
	}
	tp = append(tp, protoVarintField(2, uint64(dataType))...)
	if name != "" {
		tp = append(tp, protoString(8, name)...)
	}
	// field 9: raw_data
	if len(rawData) > 0 {
		tp = append(tp, protoBytes(9, rawData)...)
	}
	return tp
}

// buildNodeProto builds a serialized NodeProto.
func buildNodeProto(name, opType string, inputs, outputs []string, attrs []byte) []byte {
	var node []byte
	for _, inp := range inputs {
		node = append(node, protoString(1, inp)...)
	}
	for _, out := range outputs {
		node = append(node, protoString(2, out)...)
	}
	if name != "" {
		node = append(node, protoString(3, name)...)
	}
	node = append(node, protoString(4, opType)...)
	if len(attrs) > 0 {
		node = append(node, protoBytes(5, attrs)...)
	}
	return node
}

// buildAttrInt builds a serialized AttributeProto for an int value.
func buildAttrInt(name string, val int64) []byte {
	var attr []byte
	attr = append(attr, protoString(1, name)...)
	attr = append(attr, protoVarintField(3, uint64(val))...)
	attr = append(attr, protoVarintField(20, uint64(attrInt))...)
	return attr
}

// buildAttrIntList builds a serialized AttributeProto for an int list.
func buildAttrIntList(name string, vals []int64) []byte {
	var attr []byte
	attr = append(attr, protoString(1, name)...)
	attr = append(attr, protoBytes(8, encodePackedInt64(vals))...)
	attr = append(attr, protoVarintField(20, uint64(attrInts))...)
	return attr
}

// buildValueInfoProto builds a minimal ValueInfoProto (name only, no type info).
func buildValueInfoProto(name string) []byte {
	return protoString(1, name)
}

// buildGraphProto builds a serialized GraphProto.
func buildGraphProto(nodes [][]byte, initializers [][]byte, inputs, outputs []string) []byte {
	var graph []byte
	for _, n := range nodes {
		graph = append(graph, protoBytes(1, n)...)
	}
	for _, init := range initializers {
		graph = append(graph, protoBytes(5, init)...)
	}
	for _, inp := range inputs {
		graph = append(graph, protoBytes(11, buildValueInfoProto(inp))...)
	}
	for _, out := range outputs {
		graph = append(graph, protoBytes(12, buildValueInfoProto(out))...)
	}
	return graph
}

// buildModelProto builds a serialized ONNX ModelProto.
func buildModelProto(irVersion, opsetVersion int64, graphData []byte) []byte {
	var model []byte
	model = append(model, protoVarintField(1, uint64(irVersion))...)
	model = append(model, protoBytes(7, graphData)...)
	// opset_import (field 8): OperatorSetIdProto with version field 2
	opsetImport := protoVarintField(2, uint64(opsetVersion))
	model = append(model, protoBytes(8, opsetImport)...)
	return model
}

// ======================================================================
// Model parser tests with synthetic protobuf
// ======================================================================

func TestParseModelSynthetic(t *testing.T) {
	// Build a minimal model: one Relu node
	nodeData := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"Y"}, nil)
	graphData := buildGraphProto(
		[][]byte{nodeData},
		nil,
		[]string{"X"},
		[]string{"Y"},
	)
	modelData := buildModelProto(8, 13, graphData)

	model, err := ParseModel(modelData)
	if err != nil {
		t.Fatal(err)
	}
	if model.IRVersion != 8 {
		t.Errorf("ir_version: got %d, want 8", model.IRVersion)
	}
	if model.OpsetVersion != 13 {
		t.Errorf("opset: got %d, want 13", model.OpsetVersion)
	}
	if len(model.Graph.Nodes) != 1 {
		t.Fatalf("nodes: got %d, want 1", len(model.Graph.Nodes))
	}
	if model.Graph.Nodes[0].OpType != "Relu" {
		t.Errorf("op_type: got %q, want Relu", model.Graph.Nodes[0].OpType)
	}
	if model.Graph.Nodes[0].Name != "relu0" {
		t.Errorf("node name: got %q, want relu0", model.Graph.Nodes[0].Name)
	}
	if len(model.Graph.Nodes[0].Inputs) != 1 || model.Graph.Nodes[0].Inputs[0] != "X" {
		t.Errorf("node inputs: got %v, want [X]", model.Graph.Nodes[0].Inputs)
	}
	if len(model.Graph.Nodes[0].Outputs) != 1 || model.Graph.Nodes[0].Outputs[0] != "Y" {
		t.Errorf("node outputs: got %v, want [Y]", model.Graph.Nodes[0].Outputs)
	}
	if len(model.Graph.Inputs) != 1 || model.Graph.Inputs[0].Name != "X" {
		t.Errorf("graph inputs: got %v", model.Graph.Inputs)
	}
	if len(model.Graph.Outputs) != 1 || model.Graph.Outputs[0].Name != "Y" {
		t.Errorf("graph outputs: got %v", model.Graph.Outputs)
	}
}

func TestParseModelWithInitializer(t *testing.T) {
	// Model with a weight tensor initializer
	weightData := buildTensorProto("W", []int64{2, 3}, []float32{1, 2, 3, 4, 5, 6})
	nodeData := buildNodeProto("add0", "Add", []string{"X", "W"}, []string{"Y"}, nil)
	graphData := buildGraphProto(
		[][]byte{nodeData},
		[][]byte{weightData},
		[]string{"X", "W"}, // W is both input and initializer
		[]string{"Y"},
	)
	modelData := buildModelProto(9, 20, graphData)

	model, err := ParseModel(modelData)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Graph.Initializers) != 1 {
		t.Fatalf("initializers: got %d, want 1", len(model.Graph.Initializers))
	}
	init := model.Graph.Initializers[0]
	if init.Name != "W" {
		t.Errorf("initializer name: got %q, want W", init.Name)
	}
	if len(init.Dims) != 2 || init.Dims[0] != 2 || init.Dims[1] != 3 {
		t.Errorf("initializer dims: got %v, want [2,3]", init.Dims)
	}
	data, err := init.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 6 {
		t.Fatalf("initializer data len: got %d, want 6", len(data))
	}
	for i, v := range []float32{1, 2, 3, 4, 5, 6} {
		if !approxEqual(data[i], v, 1e-6) {
			t.Errorf("init data[%d]: got %f, want %f", i, data[i], v)
		}
	}
}

func TestParseModelWithNodeAttributes(t *testing.T) {
	// Conv node with kernel_shape and group attributes
	ksAttr := buildAttrIntList("kernel_shape", []int64{3, 3})
	groupAttr := buildAttrInt("group", 2)
	// Node can only have one attribute field per call, so build with multiple attrs
	nodeData := buildNodeProto("conv0", "Conv", []string{"X", "W"}, []string{"Y"}, ksAttr)
	// Append second attribute manually
	nodeData = append(nodeData, protoBytes(5, groupAttr)...)

	graphData := buildGraphProto([][]byte{nodeData}, nil, []string{"X", "W"}, []string{"Y"})
	modelData := buildModelProto(9, 20, graphData)

	model, err := ParseModel(modelData)
	if err != nil {
		t.Fatal(err)
	}
	node := model.Graph.Nodes[0]
	if len(node.Attrs) != 2 {
		t.Fatalf("attrs: got %d, want 2", len(node.Attrs))
	}

	// Find kernel_shape attr
	var foundKS, foundGroup bool
	for _, a := range node.Attrs {
		switch a.Name {
		case "kernel_shape":
			foundKS = true
			if len(a.Ints) != 2 || a.Ints[0] != 3 || a.Ints[1] != 3 {
				t.Errorf("kernel_shape: got %v, want [3,3]", a.Ints)
			}
		case "group":
			foundGroup = true
			if a.I != 2 {
				t.Errorf("group: got %d, want 2", a.I)
			}
		}
	}
	if !foundKS {
		t.Error("kernel_shape attribute not found")
	}
	if !foundGroup {
		t.Error("group attribute not found")
	}
}

func TestParseModelNoGraph(t *testing.T) {
	// Model with no graph should error
	modelData := protoVarintField(1, 9) // just ir_version
	_, err := ParseModel(modelData)
	if err == nil {
		t.Fatal("expected error for model with no graph")
	}
}

func TestTensorProtoRawDataFloat32(t *testing.T) {
	rawData := encodePackedFloat32([]float32{1.5, 2.5, 3.5})
	tp := &TensorProto{
		Dims:     []int64{3},
		DataType: onnxFloat,
		RawData:  rawData,
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	want := []float32{1.5, 2.5, 3.5}
	for i, v := range want {
		if !approxEqual(data[i], v, 1e-6) {
			t.Errorf("raw float[%d]: got %f, want %f", i, data[i], v)
		}
	}
}

func TestTensorProtoRawDataInt64(t *testing.T) {
	var rawData [16]byte
	binary.LittleEndian.PutUint64(rawData[0:], 42)
	binary.LittleEndian.PutUint64(rawData[8:], 100)
	tp := &TensorProto{
		Dims:     []int64{2},
		DataType: onnxInt64,
		RawData:  rawData[:],
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(data[0], 42, 1e-6) || !approxEqual(data[1], 100, 1e-6) {
		t.Errorf("raw int64: got %v, want [42, 100]", data)
	}
}

func TestTensorProtoRawDataInt8(t *testing.T) {
	tp := &TensorProto{
		Dims:     []int64{3},
		DataType: onnxInt8,
		RawData:  []byte{0xFF, 0x00, 0x7F}, // -1, 0, 127
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(data[0], -1, 1e-6) {
		t.Errorf("int8[0]: got %f, want -1", data[0])
	}
	if !approxEqual(data[1], 0, 1e-6) {
		t.Errorf("int8[1]: got %f, want 0", data[1])
	}
	if !approxEqual(data[2], 127, 1e-6) {
		t.Errorf("int8[2]: got %f, want 127", data[2])
	}
}

func TestTensorProtoRawDataUint8(t *testing.T) {
	tp := &TensorProto{
		Dims:     []int64{3},
		DataType: onnxUint8,
		RawData:  []byte{0, 128, 255},
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(data[0], 0, 1e-6) || !approxEqual(data[1], 128, 1e-6) || !approxEqual(data[2], 255, 1e-6) {
		t.Errorf("uint8: got %v, want [0, 128, 255]", data)
	}
}

func TestTensorProtoRawDataDouble(t *testing.T) {
	var rawData [8]byte
	binary.LittleEndian.PutUint64(rawData[:], math.Float64bits(3.14159))
	tp := &TensorProto{
		Dims:     []int64{1},
		DataType: onnxDouble,
		RawData:  rawData[:],
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(data[0], 3.14159, 1e-4) {
		t.Errorf("double: got %f, want ~3.14159", data[0])
	}
}

func TestTensorProtoRawDataInt32(t *testing.T) {
	var rawData [8]byte
	neg5 := int32(-5)
	binary.LittleEndian.PutUint32(rawData[0:], uint32(neg5))
	binary.LittleEndian.PutUint32(rawData[4:], 42)
	tp := &TensorProto{
		Dims:     []int64{2},
		DataType: onnxInt32,
		RawData:  rawData[:],
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(data[0], -5, 1e-6) || !approxEqual(data[1], 42, 1e-6) {
		t.Errorf("int32: got %v, want [-5, 42]", data)
	}
}

func TestTensorProtoUnsupportedDataType(t *testing.T) {
	tp := &TensorProto{
		Dims:     []int64{1},
		DataType: 99, // bogus
		RawData:  []byte{1, 2, 3, 4},
	}
	_, err := tp.ToFloat32()
	if err == nil {
		t.Fatal("expected error for unsupported data type")
	}
}

func TestTensorProtoEmptyTensor(t *testing.T) {
	tp := &TensorProto{
		Dims:     []int64{0},
		DataType: onnxFloat,
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("empty tensor: got %d elements, want 0", len(data))
	}
}

func TestTensorProtoInt64Data(t *testing.T) {
	tp := &TensorProto{
		Dims:      []int64{3},
		DataType:  onnxInt64,
		Int64Data: []int64{10, 20, 30},
	}
	data, err := tp.ToFloat32()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float32{10, 20, 30} {
		if !approxEqual(data[i], want, 1e-6) {
			t.Errorf("int64_data[%d]: got %f, want %f", i, data[i], want)
		}
	}
}

// ======================================================================
// Graph executor tests with synthetic models (no model file needed)
// ======================================================================

func TestSessionSyntheticRelu(t *testing.T) {
	// Build a 1-node model: X → Relu → Y
	nodeData := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"Y"}, nil)
	graphData := buildGraphProto([][]byte{nodeData}, nil, []string{"X"}, []string{"Y"})
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.InputNames()) != 1 || session.InputNames()[0] != "X" {
		t.Errorf("inputs: %v", session.InputNames())
	}
	if len(session.OutputNames()) != 1 || session.OutputNames()[0] != "Y" {
		t.Errorf("outputs: %v", session.OutputNames())
	}

	input := NewTensor([]int64{4}, []float32{-2, -1, 1, 2})
	outputs, err := session.Run(map[string]*Tensor{"X": input})
	if err != nil {
		t.Fatal(err)
	}
	output := outputs["Y"]
	assertTensorApprox(t, output, []int64{4}, []float32{0, 0, 1, 2}, eps)
}

func TestSessionSyntheticChain(t *testing.T) {
	// 3-node chain: X → Relu → temp → Sigmoid → Y
	node1 := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"temp"}, nil)
	node2 := buildNodeProto("sigmoid0", "Sigmoid", []string{"temp"}, []string{"Y"}, nil)
	graphData := buildGraphProto([][]byte{node1, node2}, nil, []string{"X"}, []string{"Y"})
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}

	input := NewTensor([]int64{3}, []float32{-10, 0, 10})
	outputs, err := session.Run(map[string]*Tensor{"X": input})
	if err != nil {
		t.Fatal(err)
	}
	output := outputs["Y"]

	// relu(-10)=0 → sigmoid(0)=0.5
	assertApprox(t, output.Data[0], 0.5, "relu→sigmoid(-10)")
	// relu(0)=0 → sigmoid(0)=0.5
	assertApprox(t, output.Data[1], 0.5, "relu→sigmoid(0)")
	// relu(10)=10 → sigmoid(10)≈1.0
	if output.Data[2] < 0.999 {
		t.Errorf("relu→sigmoid(10): got %f, want ~1.0", output.Data[2])
	}
}

func TestSessionSyntheticWithWeights(t *testing.T) {
	// Model: Y = X + W, where W is an initializer
	weightData := buildTensorProto("W", []int64{3}, []float32{10, 20, 30})
	nodeData := buildNodeProto("add0", "Add", []string{"X", "W"}, []string{"Y"}, nil)
	graphData := buildGraphProto(
		[][]byte{nodeData},
		[][]byte{weightData},
		[]string{"X", "W"},
		[]string{"Y"},
	)
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}

	// W is an initializer, so only X should be a runtime input
	if len(session.InputNames()) != 1 || session.InputNames()[0] != "X" {
		t.Errorf("expected only X as input, got %v", session.InputNames())
	}

	input := NewTensor([]int64{3}, []float32{1, 2, 3})
	outputs, err := session.Run(map[string]*Tensor{"X": input})
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, outputs["Y"], []int64{3}, []float32{11, 22, 33}, eps)
}

func TestSessionSyntheticMultiOutput(t *testing.T) {
	// Model with two outputs: X splits into A and B
	splitAttr := buildAttrIntList("split", []int64{2, 2})
	axisAttr := buildAttrInt("axis", 0)
	// Build node with two attributes
	nodeData := buildNodeProto("split0", "Split", []string{"X"}, []string{"A", "B"}, splitAttr)
	nodeData = append(nodeData, protoBytes(5, axisAttr)...)

	graphData := buildGraphProto(
		[][]byte{nodeData},
		nil,
		[]string{"X"},
		[]string{"A", "B"},
	)
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.OutputNames()) != 2 {
		t.Fatalf("outputs: got %d, want 2", len(session.OutputNames()))
	}

	input := NewTensor([]int64{4}, []float32{1, 2, 3, 4})
	outputs, err := session.Run(map[string]*Tensor{"X": input})
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, outputs["A"], []int64{2}, []float32{1, 2}, eps)
	assertTensorApprox(t, outputs["B"], []int64{2}, []float32{3, 4}, eps)
}

func TestSessionSyntheticDiamond(t *testing.T) {
	// Diamond graph: X → Relu → A, X → Sigmoid → B, A + B → Y
	node1 := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"A"}, nil)
	node2 := buildNodeProto("sigmoid0", "Sigmoid", []string{"X"}, []string{"B"}, nil)
	node3 := buildNodeProto("add0", "Add", []string{"A", "B"}, []string{"Y"}, nil)
	graphData := buildGraphProto(
		[][]byte{node1, node2, node3},
		nil,
		[]string{"X"},
		[]string{"Y"},
	)
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}

	input := NewTensor([]int64{2}, []float32{-1, 1})
	outputs, err := session.Run(map[string]*Tensor{"X": input})
	if err != nil {
		t.Fatal(err)
	}
	output := outputs["Y"]

	// For x=-1: relu(-1)=0, sigmoid(-1)≈0.2689 → sum ≈ 0.2689
	assertApprox(t, output.Data[0], 0.26894, "diamond(-1)")
	// For x=1: relu(1)=1, sigmoid(1)≈0.7311 → sum ≈ 1.7311
	assertApprox(t, output.Data[1], 1.73106, "diamond(1)")
}

func TestSessionSyntheticWithRawDataWeights(t *testing.T) {
	// Test that raw_data initializers load correctly via the full pipeline
	rawData := encodePackedFloat32([]float32{100, 200})
	weightData := buildTensorProtoRawData("W", []int64{2}, onnxFloat, rawData)
	nodeData := buildNodeProto("mul0", "Mul", []string{"X", "W"}, []string{"Y"}, nil)
	graphData := buildGraphProto(
		[][]byte{nodeData},
		[][]byte{weightData},
		[]string{"X", "W"},
		[]string{"Y"},
	)
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}

	input := NewTensor([]int64{2}, []float32{2, 3})
	outputs, err := session.Run(map[string]*Tensor{"X": input})
	if err != nil {
		t.Fatal(err)
	}
	assertTensorApprox(t, outputs["Y"], []int64{2}, []float32{200, 600}, eps)
}

func TestSessionMissingInput(t *testing.T) {
	nodeData := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"Y"}, nil)
	graphData := buildGraphProto([][]byte{nodeData}, nil, []string{"X"}, []string{"Y"})
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal(err)
	}

	// Run without providing input X
	_, err = session.Run(map[string]*Tensor{})
	if err == nil {
		t.Fatal("expected error for missing input")
	}
}

func TestSessionUnknownOp(t *testing.T) {
	nodeData := buildNodeProto("custom0", "FancyCustomOp", []string{"X"}, []string{"Y"}, nil)
	graphData := buildGraphProto([][]byte{nodeData}, nil, []string{"X"}, []string{"Y"})
	modelData := buildModelProto(9, 20, graphData)

	_, err := NewSession(modelData)
	if err == nil {
		t.Fatal("expected error for unknown operator at session init")
	}
}

// ======================================================================
// Benchmarks
// ======================================================================

func BenchmarkConv3x3_64ch_32x32(b *testing.B) {
	x := NewTensor([]int64{1, 64, 32, 32}, make([]float32, 64*32*32))
	w := NewTensor([]int64{64, 64, 3, 3}, make([]float32, 64*64*3*3))
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{3, 3}
	attrs.IntLists["pads"] = []int64{1, 1, 1, 1}
	inputs := []*Tensor{x, w}

	b.ResetTimer()
	for range b.N {
		Execute("Conv", inputs, attrs)
	}
}

func BenchmarkConv1x1_256ch_16x16(b *testing.B) {
	x := NewTensor([]int64{1, 256, 16, 16}, make([]float32, 256*16*16))
	w := NewTensor([]int64{256, 256, 1, 1}, make([]float32, 256*256))
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{1, 1}
	inputs := []*Tensor{x, w}

	b.ResetTimer()
	for range b.N {
		Execute("Conv", inputs, attrs)
	}
}

func BenchmarkMaxPool2x2_64ch_32x32(b *testing.B) {
	x := NewTensor([]int64{1, 64, 32, 32}, make([]float32, 64*32*32))
	attrs := NewAttributes()
	attrs.IntLists["kernel_shape"] = []int64{2, 2}
	attrs.IntLists["strides"] = []int64{2, 2}
	inputs := []*Tensor{x}

	b.ResetTimer()
	for range b.N {
		Execute("MaxPool", inputs, attrs)
	}
}

func BenchmarkBatchNorm_64ch_32x32(b *testing.B) {
	x := NewTensor([]int64{1, 64, 32, 32}, make([]float32, 64*32*32))
	scale := NewTensor([]int64{64}, make([]float32, 64))
	bias := NewTensor([]int64{64}, make([]float32, 64))
	mean := NewTensor([]int64{64}, make([]float32, 64))
	variance := NewTensor([]int64{64}, make([]float32, 64))
	for i := range variance.Data {
		variance.Data[i] = 1
		scale.Data[i] = 1
	}
	inputs := []*Tensor{x, scale, bias, mean, variance}

	b.ResetTimer()
	for range b.N {
		Execute("BatchNormalization", inputs, NewAttributes())
	}
}

func BenchmarkSigmoid_Large(b *testing.B) {
	x := NewTensor([]int64{1, 64, 32, 32}, make([]float32, 64*32*32))

	b.ResetTimer()
	for range b.N {
		Execute("Sigmoid", []*Tensor{x}, NewAttributes())
	}
}

func BenchmarkAdd_Broadcast(b *testing.B) {
	x := NewTensor([]int64{1, 64, 32, 32}, make([]float32, 64*32*32))
	bias := NewTensor([]int64{64, 1, 1}, make([]float32, 64))

	b.ResetTimer()
	for range b.N {
		Execute("Add", []*Tensor{x, bias}, NewAttributes())
	}
}

func BenchmarkMatMul_256x256(b *testing.B) {
	a := NewTensor([]int64{256, 256}, make([]float32, 256*256))
	bm := NewTensor([]int64{256, 256}, make([]float32, 256*256))

	b.ResetTimer()
	for range b.N {
		Execute("MatMul", []*Tensor{a, bm}, NewAttributes())
	}
}

func BenchmarkConcat_8Tensors(b *testing.B) {
	inputs := make([]*Tensor, 8)
	for i := range inputs {
		inputs[i] = NewTensor([]int64{1, 32, 16, 16}, make([]float32, 32*16*16))
	}
	attrs := NewAttributes()
	attrs.Ints["axis"] = 1

	b.ResetTimer()
	for range b.N {
		Execute("Concat", inputs, attrs)
	}
}

func BenchmarkSyntheticInference3Nodes(b *testing.B) {
	// Small synthetic model: X → Relu → Sigmoid → Add(with weights) → Y
	weightData := buildTensorProto("W", []int64{1024}, make([]float32, 1024))
	node1 := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"t1"}, nil)
	node2 := buildNodeProto("sig0", "Sigmoid", []string{"t1"}, []string{"t2"}, nil)
	node3 := buildNodeProto("add0", "Add", []string{"t2", "W"}, []string{"Y"}, nil)
	graphData := buildGraphProto(
		[][]byte{node1, node2, node3},
		[][]byte{weightData},
		[]string{"X", "W"},
		[]string{"Y"},
	)
	modelData := buildModelProto(9, 20, graphData)

	session, err := NewSession(modelData)
	if err != nil {
		b.Fatal(err)
	}
	input := NewTensor([]int64{1024}, make([]float32, 1024))

	b.ResetTimer()
	for range b.N {
		session.Run(map[string]*Tensor{"X": input})
	}
}
