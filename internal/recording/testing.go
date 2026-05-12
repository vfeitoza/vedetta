package recording

// LockForTest and UnlockForTest expose segmentOpMu for tests that need
// to simulate an in-flight storage operation (e.g. asserting 409
// responses on /api/storage endpoints). Not for production use.
func (r *Recorder) LockForTest()   { r.segmentOpMu.Lock() }
func (r *Recorder) UnlockForTest() { r.segmentOpMu.Unlock() }

// Path returns the root recording directory configured for this Recorder.
// Used by tests that need to write real files into the recording tree.
func (r *Recorder) Path() string { return r.config.Path }
