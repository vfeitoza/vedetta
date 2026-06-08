package api

import "testing"

// The reconcile entry points must be safe when subsystems are absent - API
// tests construct a Server with a nil hub/cameras, and the setup-transition
// path calls SetSubsystems before SetContext.
func TestWarmReconcileNilSafe(t *testing.T) {
	s := &Server{}
	s.reconcileWarmHLS()      // must not panic
	s.startWarmHLSReconcile() // must not panic, must not spawn a loop
}
