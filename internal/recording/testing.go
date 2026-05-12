package recording

import "github.com/rvben/vedetta/internal/media"

// LockForTest and UnlockForTest expose segmentOpMu for tests that need
// to simulate an in-flight storage operation (e.g. asserting 409
// responses on /api/storage endpoints). Not for production use.
func (r *Recorder) LockForTest()   { r.segmentOpMu.Lock() }
func (r *Recorder) UnlockForTest() { r.segmentOpMu.Unlock() }

// Path returns the root recording directory configured for this Recorder.
// Used by tests that need to write real files into the recording tree.
func (r *Recorder) Path() string { return r.config.Path }

// RegisterFakeOpenConsumer wires a synthetic consumer whose
// CurrentSegmentPath() returns path. Used by tests for the open-segment
// protection path.
func (r *Recorder) RegisterFakeOpenConsumer(camera, path string) {
	rc := &media.RecordingConsumer{}
	rc.SetTestState(camera, path)
	r.segments.mu.Lock()
	r.segments.consumers = append(r.segments.consumers, rc)
	r.segments.mu.Unlock()
}
