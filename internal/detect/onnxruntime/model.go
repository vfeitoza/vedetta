package onnxruntime

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ONNX data types (from onnx.proto TensorProto.DataType)
const (
	onnxFloat  = 1
	onnxUint8  = 2
	onnxInt8   = 3
	onnxInt32  = 6
	onnxInt64  = 7
	onnxDouble = 11
)

// ONNX attribute types (from onnx.proto AttributeProto.AttributeType)
const (
	attrFloat     = 1
	attrInt       = 2
	attrString    = 3
	attrTensor    = 4
	attrFloats    = 6
	attrInts      = 7
	attrStrings   = 8
)

// ModelProto is the top-level ONNX model container.
type ModelProto struct {
	IRVersion    int64
	OpsetVersion int64
	Graph        *GraphProto
}

// GraphProto contains the computation graph.
type GraphProto struct {
	Nodes        []*NodeProto
	Initializers []*TensorProto
	Inputs       []*ValueInfoProto
	Outputs      []*ValueInfoProto
}

// NodeProto represents a single operation in the graph.
type NodeProto struct {
	Inputs   []string
	Outputs  []string
	Name     string
	OpType   string
	Attrs    []*AttributeProto
}

// AttributeProto holds an operator attribute.
type AttributeProto struct {
	Name   string
	Type   int32
	F      float32
	I      int64
	S      []byte
	T      *TensorProto
	Floats []float32
	Ints   []int64
}

// TensorProto holds a tensor (weights/constants).
type TensorProto struct {
	Name     string
	Dims     []int64
	DataType int32
	RawData  []byte
	FloatData []float32
	Int64Data []int64
	Int32Data []int32
}

// ValueInfoProto describes a graph input/output.
type ValueInfoProto struct {
	Name  string
	Shape []int64
}

// ParseModel parses an ONNX model from raw protobuf bytes.
func ParseModel(data []byte) (*ModelProto, error) {
	model := &ModelProto{}
	r := newProtoReader(data)

	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, fmt.Errorf("model: %w", err)
		}

		switch {
		case fieldNum == 1 && wireType == wireVarint: // ir_version
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			model.IRVersion = int64(v)

		case fieldNum == 7 && wireType == wireBytes: // graph
			graphData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			graph, err := parseGraphProto(graphData)
			if err != nil {
				return nil, fmt.Errorf("model.graph: %w", err)
			}
			model.Graph = graph

		case fieldNum == 8 && wireType == wireBytes: // opset_import
			opsetData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			// Parse opset version from OperatorSetIdProto
			opR := newProtoReader(opsetData)
			for opR.remaining() > 0 {
				fn, wt, err := opR.readTag()
				if err != nil {
					break
				}
				if fn == 2 && wt == wireVarint {
					v, _ := opR.readVarint()
					model.OpsetVersion = int64(v)
				} else {
					opR.skip(wt)
				}
			}

		default:
			if err := r.skip(wireType); err != nil {
				return nil, err
			}
		}
	}

	if model.Graph == nil {
		return nil, fmt.Errorf("model: no graph found")
	}
	return model, nil
}

func parseGraphProto(data []byte) (*GraphProto, error) {
	graph := &GraphProto{}
	r := newProtoReader(data)

	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}

		switch {
		case fieldNum == 1 && wireType == wireBytes: // node
			nodeData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			node, err := parseNodeProto(nodeData)
			if err != nil {
				return nil, fmt.Errorf("graph.node: %w", err)
			}
			graph.Nodes = append(graph.Nodes, node)

		case fieldNum == 5 && wireType == wireBytes: // initializer
			tensorData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			tensor, err := parseTensorProto(tensorData)
			if err != nil {
				return nil, fmt.Errorf("graph.initializer: %w", err)
			}
			graph.Initializers = append(graph.Initializers, tensor)

		case fieldNum == 11 && wireType == wireBytes: // input
			viData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			vi, err := parseValueInfoProto(viData)
			if err != nil {
				return nil, err
			}
			graph.Inputs = append(graph.Inputs, vi)

		case fieldNum == 12 && wireType == wireBytes: // output
			viData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			vi, err := parseValueInfoProto(viData)
			if err != nil {
				return nil, err
			}
			graph.Outputs = append(graph.Outputs, vi)

		default:
			if err := r.skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return graph, nil
}

func parseNodeProto(data []byte) (*NodeProto, error) {
	node := &NodeProto{}
	r := newProtoReader(data)

	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}

		switch {
		case fieldNum == 1 && wireType == wireBytes: // input
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			node.Inputs = append(node.Inputs, string(b))

		case fieldNum == 2 && wireType == wireBytes: // output
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			node.Outputs = append(node.Outputs, string(b))

		case fieldNum == 3 && wireType == wireBytes: // name
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			node.Name = string(b)

		case fieldNum == 4 && wireType == wireBytes: // op_type
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			node.OpType = string(b)

		case fieldNum == 5 && wireType == wireBytes: // attribute
			attrData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			attr, err := parseAttributeProto(attrData)
			if err != nil {
				return nil, err
			}
			node.Attrs = append(node.Attrs, attr)

		default:
			if err := r.skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}

func parseAttributeProto(data []byte) (*AttributeProto, error) {
	attr := &AttributeProto{}
	r := newProtoReader(data)

	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}

		switch {
		case fieldNum == 1 && wireType == wireBytes: // name
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			attr.Name = string(b)

		case fieldNum == 2 && wireType == wire32Bit: // f (float)
			v, err := r.readFixed32()
			if err != nil {
				return nil, err
			}
			attr.F = math.Float32frombits(v)

		case fieldNum == 3 && wireType == wireVarint: // i (int64)
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			attr.I = int64(v)

		case fieldNum == 4 && wireType == wireBytes: // s (bytes/string)
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			attr.S = b

		case fieldNum == 5 && wireType == wireBytes: // t (tensor)
			tensorData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			tensor, err := parseTensorProto(tensorData)
			if err != nil {
				return nil, err
			}
			attr.T = tensor

		case fieldNum == 7 && wireType == wireBytes: // floats (packed)
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			attr.Floats = readPackedFloat32(b)

		case fieldNum == 7 && wireType == wire32Bit: // floats (unpacked)
			v, err := r.readFixed32()
			if err != nil {
				return nil, err
			}
			attr.Floats = append(attr.Floats, math.Float32frombits(v))

		case fieldNum == 8 && wireType == wireBytes: // ints (packed)
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			ints, err := readPackedInt64(b)
			if err != nil {
				return nil, err
			}
			attr.Ints = ints

		case fieldNum == 8 && wireType == wireVarint: // ints (unpacked)
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			attr.Ints = append(attr.Ints, int64(v))

		case fieldNum == 20 && wireType == wireVarint: // type
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			attr.Type = int32(v)

		default:
			if err := r.skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return attr, nil
}

func parseTensorProto(data []byte) (*TensorProto, error) {
	tp := &TensorProto{}
	r := newProtoReader(data)

	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}

		switch {
		case fieldNum == 1 && wireType == wireBytes: // dims (packed)
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			dims, err := readPackedInt64(b)
			if err != nil {
				return nil, err
			}
			tp.Dims = append(tp.Dims, dims...)

		case fieldNum == 1 && wireType == wireVarint: // dims (unpacked)
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			tp.Dims = append(tp.Dims, int64(v))

		case fieldNum == 2 && wireType == wireVarint: // data_type
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			tp.DataType = int32(v)

		case fieldNum == 4 && wireType == wireBytes: // float_data (packed)
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			tp.FloatData = append(tp.FloatData, readPackedFloat32(b)...)

		case fieldNum == 4 && wireType == wire32Bit: // float_data (unpacked)
			v, err := r.readFixed32()
			if err != nil {
				return nil, err
			}
			tp.FloatData = append(tp.FloatData, math.Float32frombits(v))

		case fieldNum == 8 && wireType == wireBytes: // name
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			tp.Name = string(b)

		case fieldNum == 9 && wireType == wireBytes: // raw_data
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			tp.RawData = b

		case fieldNum == 7 && wireType == wireBytes: // int64_data (packed)
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			ints, err := readPackedInt64(b)
			if err != nil {
				return nil, err
			}
			tp.Int64Data = append(tp.Int64Data, ints...)

		case fieldNum == 7 && wireType == wireVarint: // int64_data (unpacked)
			v, err := r.readVarint()
			if err != nil {
				return nil, err
			}
			tp.Int64Data = append(tp.Int64Data, int64(v))

		default:
			if err := r.skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return tp, nil
}

func parseValueInfoProto(data []byte) (*ValueInfoProto, error) {
	vi := &ValueInfoProto{}
	r := newProtoReader(data)

	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}

		switch {
		case fieldNum == 1 && wireType == wireBytes: // name
			b, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			vi.Name = string(b)

		case fieldNum == 2 && wireType == wireBytes: // type (TypeProto)
			typeData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			shape, err := parseTypeProtoShape(typeData)
			if err == nil {
				vi.Shape = shape
			}

		default:
			if err := r.skip(wireType); err != nil {
				return nil, err
			}
		}
	}
	return vi, nil
}

// parseTypeProtoShape extracts the shape from a TypeProto message.
// TypeProto → tensor_type (field 1) → TensorTypeProto → shape (field 2) → TensorShapeProto → dim[]
func parseTypeProtoShape(data []byte) ([]int64, error) {
	r := newProtoReader(data)
	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if fieldNum == 1 && wireType == wireBytes { // tensor_type
			ttData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			return parseTensorTypeShape(ttData)
		}
		r.skip(wireType)
	}
	return nil, nil
}

func parseTensorTypeShape(data []byte) ([]int64, error) {
	r := newProtoReader(data)
	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if fieldNum == 2 && wireType == wireBytes { // shape
			shapeData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			return parseTensorShapeProto(shapeData)
		}
		r.skip(wireType)
	}
	return nil, nil
}

func parseTensorShapeProto(data []byte) ([]int64, error) {
	var dims []int64
	r := newProtoReader(data)
	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if fieldNum == 1 && wireType == wireBytes { // dim (repeated Dimension)
			dimData, err := r.readBytes()
			if err != nil {
				return nil, err
			}
			dim, err := parseDimensionProto(dimData)
			if err != nil {
				return nil, err
			}
			dims = append(dims, dim)
		} else {
			r.skip(wireType)
		}
	}
	return dims, nil
}

func parseDimensionProto(data []byte) (int64, error) {
	r := newProtoReader(data)
	for r.remaining() > 0 {
		fieldNum, wireType, err := r.readTag()
		if err != nil {
			return 0, err
		}
		if fieldNum == 1 && wireType == wireVarint { // dim_value
			v, err := r.readVarint()
			if err != nil {
				return 0, err
			}
			return int64(v), nil
		}
		r.skip(wireType)
	}
	return 0, nil
}

// ToFloat32 converts a TensorProto to a float32 slice, handling all common data types.
func (tp *TensorProto) ToFloat32() ([]float32, error) {
	// float_data takes precedence if present
	if len(tp.FloatData) > 0 {
		return tp.FloatData, nil
	}

	if len(tp.RawData) == 0 {
		// Might be an empty tensor or int64_data
		if len(tp.Int64Data) > 0 {
			result := make([]float32, len(tp.Int64Data))
			for i, v := range tp.Int64Data {
				result[i] = float32(v)
			}
			return result, nil
		}
		return []float32{}, nil
	}

	switch tp.DataType {
	case onnxFloat:
		return readPackedFloat32(tp.RawData), nil

	case onnxDouble:
		n := len(tp.RawData) / 8
		result := make([]float32, n)
		for i := range n {
			bits := binary.LittleEndian.Uint64(tp.RawData[i*8:])
			result[i] = float32(math.Float64frombits(bits))
		}
		return result, nil

	case onnxInt32:
		n := len(tp.RawData) / 4
		result := make([]float32, n)
		for i := range n {
			v := int32(binary.LittleEndian.Uint32(tp.RawData[i*4:]))
			result[i] = float32(v)
		}
		return result, nil

	case onnxInt64:
		n := len(tp.RawData) / 8
		result := make([]float32, n)
		for i := range n {
			v := int64(binary.LittleEndian.Uint64(tp.RawData[i*8:]))
			result[i] = float32(v)
		}
		return result, nil

	case onnxInt8:
		result := make([]float32, len(tp.RawData))
		for i, b := range tp.RawData {
			result[i] = float32(int8(b))
		}
		return result, nil

	case onnxUint8:
		result := make([]float32, len(tp.RawData))
		for i, b := range tp.RawData {
			result[i] = float32(b)
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported tensor data type: %d", tp.DataType)
	}
}
