package onnxruntime

import (
	"os"
	"testing"
)

func TestParseYOLOv8nModel(t *testing.T) {
	modelData, err := os.ReadFile("/tmp/yolov8n.onnx")
	if err != nil {
		t.Skip("YOLOv8n model not found at /tmp/yolov8n.onnx")
	}

	model, err := ParseModel(modelData)
	if err != nil {
		t.Fatal("parse model:", err)
	}

	if model.Graph == nil {
		t.Fatal("model has no graph")
	}

	t.Logf("IR version: %d", model.IRVersion)
	t.Logf("Opset: %d", model.OpsetVersion)
	t.Logf("Nodes: %d", len(model.Graph.Nodes))
	t.Logf("Initializers: %d", len(model.Graph.Initializers))
	t.Logf("Inputs: %d", len(model.Graph.Inputs))
	t.Logf("Outputs: %d", len(model.Graph.Outputs))

	if len(model.Graph.Nodes) < 200 {
		t.Errorf("expected 200+ nodes, got %d", len(model.Graph.Nodes))
	}

	// Check all required ops are registered
	missingOps := map[string]bool{}
	for _, node := range model.Graph.Nodes {
		if _, ok := Registry[node.OpType]; !ok {
			missingOps[node.OpType] = true
		}
	}
	if len(missingOps) > 0 {
		t.Fatalf("missing operator implementations: %v", missingOps)
	}
}

func TestLoadYOLOv8nSession(t *testing.T) {
	modelData, err := os.ReadFile("/tmp/yolov8n.onnx")
	if err != nil {
		t.Skip("YOLOv8n model not found at /tmp/yolov8n.onnx")
	}

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal("create session:", err)
	}

	if len(session.InputNames()) != 1 {
		t.Errorf("expected 1 input, got %d", len(session.InputNames()))
	}
	if session.InputNames()[0] != "images" {
		t.Errorf("expected input name 'images', got %q", session.InputNames()[0])
	}

	if len(session.OutputNames()) != 1 {
		t.Errorf("expected 1 output, got %d", len(session.OutputNames()))
	}
	if session.OutputNames()[0] != "output0" {
		t.Errorf("expected output name 'output0', got %q", session.OutputNames()[0])
	}
}

func TestRunYOLOv8nInference(t *testing.T) {
	modelData, err := os.ReadFile("/tmp/yolov8n.onnx")
	if err != nil {
		t.Skip("YOLOv8n model not found at /tmp/yolov8n.onnx")
	}

	session, err := NewSession(modelData)
	if err != nil {
		t.Fatal("create session:", err)
	}

	// Create a zero-filled input tensor [1, 3, 640, 640]
	input := NewTensor([]int64{1, 3, 640, 640}, nil)

	t.Log("running inference on zero-filled 640x640 input...")
	outputs, err := session.Run(map[string]*Tensor{
		"images": input,
	})
	if err != nil {
		t.Fatal("inference failed:", err)
	}

	output, ok := outputs["output0"]
	if !ok {
		t.Fatal("output 'output0' not found")
	}

	t.Logf("output shape: %v", output.Shape)
	t.Logf("output size: %d", len(output.Data))

	// YOLOv8n output should be [1, 84, 8400]
	if len(output.Shape) != 3 {
		t.Fatalf("expected 3D output, got %dD: %v", len(output.Shape), output.Shape)
	}
	if output.Shape[0] != 1 {
		t.Errorf("output batch: got %d, want 1", output.Shape[0])
	}
	if output.Shape[1] != 84 {
		t.Errorf("output attributes: got %d, want 84", output.Shape[1])
	}
	if output.Shape[2] != 8400 {
		t.Errorf("output detections: got %d, want 8400", output.Shape[2])
	}

	// Sanity: output should contain finite values
	for i, v := range output.Data {
		if v != v { // NaN check
			t.Fatalf("output contains NaN at index %d", i)
		}
		if i > 1000 {
			break // spot check is sufficient
		}
	}

	t.Log("inference completed successfully")
}
