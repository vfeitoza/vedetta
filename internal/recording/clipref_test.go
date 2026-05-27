package recording

import (
	"errors"
	"testing"
)

// stubClipRefClearer records calls and returns the configured errors so we can
// assert that clearClipRef and clearSnapshotRef surface (rather than discard) DB failures.
type stubClipRefClearer struct {
	clearClipCalls int
	clearClipErr   error
	snapPathErr    error
	snapAvailErr   error
	calls          []string
}

func (s *stubClipRefClearer) ClearEventClip(eventID string) error {
	s.clearClipCalls++
	return s.clearClipErr
}

func (s *stubClipRefClearer) UpdateEventSnapshotPath(eventID, snapshotPath string) error {
	s.calls = append(s.calls, "snapPath")
	return s.snapPathErr
}

func (s *stubClipRefClearer) UpdateEventSnapshotAvailability(eventID string, available bool) error {
	s.calls = append(s.calls, "snapAvail")
	return s.snapAvailErr
}

func TestClearClipRef_UsesClearEventClip(t *testing.T) {
	stub := &stubClipRefClearer{}
	if err := clearClipRef(stub, "ev1"); err != nil {
		t.Fatalf("clearClipRef: %v", err)
	}
	if stub.clearClipCalls != 1 {
		t.Errorf("ClearEventClip called %d times, want 1", stub.clearClipCalls)
	}
}

func TestClearClipRef_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	stub := &stubClipRefClearer{clearClipErr: wantErr}
	if err := clearClipRef(stub, "ev1"); !errors.Is(err, wantErr) {
		t.Errorf("clearClipRef err = %v, want %v", err, wantErr)
	}
}

func TestClearSnapshotRef_JoinsBothUpdateErrors(t *testing.T) {
	errPath := errors.New("snap path write failed")
	errAvail := errors.New("snap availability write failed")
	stub := &stubClipRefClearer{snapPathErr: errPath, snapAvailErr: errAvail}

	err := clearSnapshotRef(stub, "evt-2")
	if err == nil {
		t.Fatal("clearSnapshotRef discarded the DB errors; want a non-nil error")
	}
	if !errors.Is(err, errPath) {
		t.Errorf("returned error does not wrap the snapshot-path error: %v", err)
	}
	if !errors.Is(err, errAvail) {
		t.Errorf("returned error does not wrap the snapshot-availability error: %v", err)
	}
}

func TestClearSnapshotRef_SuccessReturnsNilAndCallsBoth(t *testing.T) {
	stub := &stubClipRefClearer{}
	if err := clearSnapshotRef(stub, "evt-2"); err != nil {
		t.Fatalf("clearSnapshotRef on healthy DB returned %v, want nil", err)
	}
	if len(stub.calls) != 2 || stub.calls[0] != "snapPath" || stub.calls[1] != "snapAvail" {
		t.Errorf("expected snapPath then snapAvail calls, got %v", stub.calls)
	}
}
