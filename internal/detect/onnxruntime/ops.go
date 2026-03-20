package onnxruntime

import "fmt"

// OpFunc executes an ONNX operator given input tensors and attributes.
type OpFunc func(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error)

// Attributes holds the parsed attributes of an ONNX node.
type Attributes struct {
	Ints       map[string]int64
	Floats     map[string]float32
	Strings    map[string]string
	IntLists   map[string][]int64
	FloatLists map[string][]float32
	Tensors    map[string]*Tensor
}

func NewAttributes() *Attributes {
	return &Attributes{
		Ints:       make(map[string]int64),
		Floats:     make(map[string]float32),
		Strings:    make(map[string]string),
		IntLists:   make(map[string][]int64),
		FloatLists: make(map[string][]float32),
		Tensors:    make(map[string]*Tensor),
	}
}

// GetInt returns an int attribute or a default value.
func (a *Attributes) GetInt(name string, defaultVal int64) int64 {
	if v, ok := a.Ints[name]; ok {
		return v
	}
	return defaultVal
}

// GetIntList returns an int list attribute or nil.
func (a *Attributes) GetIntList(name string) []int64 {
	return a.IntLists[name]
}

// GetFloat returns a float attribute or a default value.
func (a *Attributes) GetFloat(name string, defaultVal float32) float32 {
	if v, ok := a.Floats[name]; ok {
		return v
	}
	return defaultVal
}

// GetString returns a string attribute or a default value.
func (a *Attributes) GetString(name string, defaultVal string) string {
	if v, ok := a.Strings[name]; ok {
		return v
	}
	return defaultVal
}

// GetTensor returns a tensor attribute or nil.
func (a *Attributes) GetTensor(name string) *Tensor {
	return a.Tensors[name]
}

// Registry maps ONNX op type names to their Go implementations.
var Registry = map[string]OpFunc{}

// Register adds an operator implementation to the registry.
func Register(opType string, fn OpFunc) {
	Registry[opType] = fn
}

// Execute runs a registered operator.
func Execute(opType string, inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	fn, ok := Registry[opType]
	if !ok {
		return nil, fmt.Errorf("unsupported ONNX operator: %s", opType)
	}
	return fn(inputs, attrs)
}
