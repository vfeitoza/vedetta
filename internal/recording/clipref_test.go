package recording

import (
	"errors"
	"testing"
)

// stubClipRefClearer records calls and returns the configured errors so we can
// assert that clearMediaRefs surfaces (rather than discards) DB failures.
type stubClipRefClearer struct {
	clipPathErr  error
	clipAvailErr error
	snapPathErr  error
	snapAvailErr error
	calls        []string
}

func (s *stubClipRefClearer) UpdateEventClipPath(eventID, clipPath string) error {
	s.calls = append(s.calls, "clipPath")
	return s.clipPathErr
}

func (s *stubClipRefClearer) UpdateEventClipAvailability(eventID string, available bool) error {
	s.calls = append(s.calls, "clipAvail")
	return s.clipAvailErr
}

func (s *stubClipRefClearer) UpdateEventSnapshotPath(eventID, snapshotPath string) error {
	s.calls = append(s.calls, "snapPath")
	return s.snapPathErr
}

func (s *stubClipRefClearer) UpdateEventSnapshotAvailability(eventID string, available bool) error {
	s.calls = append(s.calls, "snapAvail")
	return s.snapAvailErr
}

func TestClearClipRef_JoinsBothUpdateErrors(t *testing.T) {
	errPath := errors.New("clip path write failed")
	errAvail := errors.New("clip availability write failed")
	stub := &stubClipRefClearer{clipPathErr: errPath, clipAvailErr: errAvail}

	err := clearClipRef(stub, "evt-1")
	if err == nil {
		t.Fatal("clearClipRef discarded the DB errors; want a non-nil error")
	}
	if !errors.Is(err, errPath) {
		t.Errorf("returned error does not wrap the clip-path error: %v", err)
	}
	if !errors.Is(err, errAvail) {
		t.Errorf("returned error does not wrap the clip-availability error: %v", err)
	}
}

func TestClearClipRef_SuccessReturnsNilAndCallsBoth(t *testing.T) {
	stub := &stubClipRefClearer{}
	if err := clearClipRef(stub, "evt-1"); err != nil {
		t.Fatalf("clearClipRef on healthy DB returned %v, want nil", err)
	}
	if len(stub.calls) != 2 || stub.calls[0] != "clipPath" || stub.calls[1] != "clipAvail" {
		t.Errorf("expected clipPath then clipAvail calls, got %v", stub.calls)
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
