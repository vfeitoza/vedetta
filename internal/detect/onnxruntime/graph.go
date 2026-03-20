package onnxruntime

import (
	"fmt"
	"log/slog"
)

// Session holds a loaded ONNX model ready for inference.
type Session struct {
	model       *ModelProto
	inputNames  []string
	outputNames []string
	// Execution order: nodes sorted so all inputs are available before use.
	execOrder []*NodeProto
	// initializers holds pre-loaded weight tensors by name.
	initializers map[string]*Tensor
}

// NewSession loads an ONNX model from raw bytes and prepares it for inference.
func NewSession(modelData []byte) (*Session, error) {
	model, err := ParseModel(modelData)
	if err != nil {
		return nil, fmt.Errorf("parse model: %w", err)
	}

	s := &Session{
		model:        model,
		initializers: make(map[string]*Tensor),
	}

	// Load initializers (model weights)
	for _, init := range model.Graph.Initializers {
		data, err := init.ToFloat32()
		if err != nil {
			return nil, fmt.Errorf("load initializer %q: %w", init.Name, err)
		}
		s.initializers[init.Name] = NewTensor(init.Dims, data)
	}

	// Collect input/output names (inputs that aren't initializers are model inputs)
	for _, inp := range model.Graph.Inputs {
		if _, isInit := s.initializers[inp.Name]; !isInit {
			s.inputNames = append(s.inputNames, inp.Name)
		}
	}
	for _, out := range model.Graph.Outputs {
		s.outputNames = append(s.outputNames, out.Name)
	}

	// Topological sort (ONNX guarantees nodes are already in topological order,
	// so we just verify and use the existing order)
	s.execOrder = model.Graph.Nodes

	slog.Info("ONNX session loaded",
		"nodes", len(s.execOrder),
		"initializers", len(s.initializers),
		"inputs", s.inputNames,
		"outputs", s.outputNames,
		"opset", model.OpsetVersion,
	)

	return s, nil
}

// InputNames returns the model input tensor names.
func (s *Session) InputNames() []string {
	return s.inputNames
}

// OutputNames returns the model output tensor names.
func (s *Session) OutputNames() []string {
	return s.outputNames
}

// Run executes the model with the given input tensors and returns the output tensors.
func (s *Session) Run(inputs map[string]*Tensor) (map[string]*Tensor, error) {
	// Value store: maps tensor names to their values
	values := make(map[string]*Tensor, len(s.initializers)+len(inputs)+len(s.execOrder))

	// Pre-populate with initializers (weights)
	for name, t := range s.initializers {
		values[name] = t
	}

	// Pre-populate with user inputs
	for name, t := range inputs {
		values[name] = t
	}

	// Execute nodes in order
	for i, node := range s.execOrder {
		// Gather inputs
		nodeInputs := make([]*Tensor, len(node.Inputs))
		for j, name := range node.Inputs {
			if name == "" {
				// Optional input, left as nil
				continue
			}
			t, ok := values[name]
			if !ok {
				return nil, fmt.Errorf("node %d (%s/%s): input %q not found", i, node.Name, node.OpType, name)
			}
			nodeInputs[j] = t
		}

		// Parse attributes
		attrs := nodeAttrsToAttributes(node.Attrs)

		// Execute
		outputs, err := Execute(node.OpType, nodeInputs, attrs)
		if err != nil {
			return nil, fmt.Errorf("node %d (%s/%s): %w", i, node.Name, node.OpType, err)
		}

		// Store outputs
		for j, name := range node.Outputs {
			if j < len(outputs) && name != "" {
				values[name] = outputs[j]
			}
		}
	}

	// Collect requested outputs
	result := make(map[string]*Tensor, len(s.outputNames))
	for _, name := range s.outputNames {
		t, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("output %q not produced by graph", name)
		}
		result[name] = t
	}

	return result, nil
}

// nodeAttrsToAttributes converts ONNX proto attributes to our Attributes type.
func nodeAttrsToAttributes(protoAttrs []*AttributeProto) *Attributes {
	attrs := NewAttributes()
	for _, a := range protoAttrs {
		switch {
		case a.Type == attrFloat || (a.Type == 0 && a.F != 0):
			attrs.Floats[a.Name] = a.F
		case a.Type == attrInt || (a.Type == 0 && a.I != 0):
			attrs.Ints[a.Name] = a.I
		case a.Type == attrString || (a.Type == 0 && len(a.S) > 0):
			attrs.Strings[a.Name] = string(a.S)
		case a.Type == attrTensor:
			if a.T != nil {
				data, err := a.T.ToFloat32()
				if err == nil {
					attrs.Tensors[a.Name] = NewTensor(a.T.Dims, data)
				}
			}
		case a.Type == attrFloats:
			attrs.FloatLists[a.Name] = a.Floats
		case a.Type == attrInts:
			attrs.IntLists[a.Name] = a.Ints
		}
	}
	return attrs
}
