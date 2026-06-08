package api

import (
	"context"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/stream"
)

// The reconcile entry points must be safe when subsystems are absent - API
// tests construct a Server with a nil hub/cameras.
func TestWarmReconcileNilSafe(t *testing.T) {
	s := &Server{}
	s.reconcileWarmHLS()      // must not panic
	s.startWarmHLSReconcile() // must not panic, must not spawn a loop
}

// The setup-mode transition path runs SetSubsystems before SetContext, so
// s.ctx is nil even though hub/cameras/hls are set. The reconcile goroutine
// must not dereference a nil s.ctx.
func TestStartWarmHLSReconcileNilCtxNoPanic(t *testing.T) {
	hub := rtsp.NewHub(context.Background())
	hls := stream.NewHLSManager(hub)
	defer hls.Close()

	s := &Server{hub: hub, cameras: &camera.Manager{}, hls: hls} // ctx deliberately nil
	s.startWarmHLSReconcile()
	// Let the goroutine run its initial reconcile and enter the select. If it
	// dereferenced a nil s.ctx it would panic and crash the test process.
	time.Sleep(20 * time.Millisecond)
}
