package onnxruntime

import "fmt"

func init() {
	Register("Constant", opConstant)
}

func opConstant(inputs []*Tensor, attrs *Attributes) ([]*Tensor, error) {
	// Tensor value takes priority
	if t := attrs.GetTensor("value"); t != nil {
		return []*Tensor{t.Clone()}, nil
	}

	// Scalar float
	if v, ok := attrs.Floats["value_float"]; ok {
		return []*Tensor{ScalarTensor(v)}, nil
	}

	// Scalar int
	if v, ok := attrs.Ints["value_int"]; ok {
		return []*Tensor{ScalarTensor(float32(v))}, nil
	}

	// Float list
	if v, ok := attrs.FloatLists["value_floats"]; ok {
		t := NewTensor([]int64{int64(len(v))}, nil)
		copy(t.Data, v)
		return []*Tensor{t}, nil
	}

	// Int list
	if v, ok := attrs.IntLists["value_ints"]; ok {
		data := make([]float32, len(v))
		for i, val := range v {
			data[i] = float32(val)
		}
		t := NewTensor([]int64{int64(len(v))}, data)
		return []*Tensor{t}, nil
	}

	return nil, fmt.Errorf("constant: no value attribute found")
}
