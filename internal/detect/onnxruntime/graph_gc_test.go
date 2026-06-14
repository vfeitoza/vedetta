package onnxruntime

import (
	"runtime/debug"
	"sync"
	"testing"
)

// TestSessionRunPreservesGlobalGC asserts that running inference never mutates
// the process-global GC target. Each camera owns its own Session and they run
// concurrently (internal/detect/backend.go: "Each goroutine must use its own
// Backend"), so any per-call save/restore of a global GC setting races: one
// goroutine can read a value another goroutine already lowered, then "restore"
// that lowered value, latching GC off for the whole process until the next
// restart. With GC latched off, the heap ramps unbounded until the memory guard
// trips. Inference must therefore leave debug.SetGCPercent untouched.
func TestSessionRunPreservesGlobalGC(t *testing.T) {
	// Pin a known GC target and restore the real default afterwards, even if the
	// code under test latches it off.
	original := debug.SetGCPercent(100)
	t.Cleanup(func() { debug.SetGCPercent(original) })

	const (
		sessions   = 8
		iterations = 3000
	)

	run := make([]func(), sessions)
	for i := range run {
		nodeData := buildNodeProto("relu0", "Relu", []string{"X"}, []string{"Y"}, nil)
		graphData := buildGraphProto([][]byte{nodeData}, nil, []string{"X"}, []string{"Y"})
		modelData := buildModelProto(9, 20, graphData)
		session, err := NewSession(modelData)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		run[i] = func() {
			input := NewTensor([]int64{4}, []float32{-2, -1, 1, 2})
			if _, err := session.Run(map[string]*Tensor{"X": input}); err != nil {
				t.Errorf("Run: %v", err)
			}
		}
	}

	var wg sync.WaitGroup
	for i := range run {
		wg.Add(1)
		go func(r func()) {
			defer wg.Done()
			for range iterations {
				r()
			}
		}(run[i])
	}
	wg.Wait()

	// Read the live GC target without leaving it changed.
	got := debug.SetGCPercent(100)
	if got != 100 {
		t.Fatalf("Session.Run left GOGC at %d, want 100: inference must not mutate the process-global GC setting", got)
	}
}
