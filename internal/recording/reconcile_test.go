package recording

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rvben/vedetta/internal/storage"
)

type mediaWrite struct {
	id   string
	snap bool
	clip bool
}

// fakeMediaStore records every availability write so tests can assert that
// reconciliation skips rows whose availability did not change.
type fakeMediaStore struct {
	refs   []storage.EventMediaRef
	writes []mediaWrite
}

func (f *fakeMediaStore) EventMediaRefs() ([]storage.EventMediaRef, error) {
	return f.refs, nil
}

func (f *fakeMediaStore) UpdateEventMediaAvailability(id string, snapshotAvailable, clipAvailable bool) error {
	f.writes = append(f.writes, mediaWrite{id: id, snap: snapshotAvailable, clip: clipAvailable})
	return nil
}

// TestReconcile_SkipsUnchanged verifies the dominant steady-state path: when the
// on-disk availability matches the stored flags, no UPDATE is issued. This is
// the fix for write amplification — every media-bearing event was rewritten
// twice on every pass regardless of whether anything changed.
func TestReconcile_SkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "snap.jpg")
	if err := os.WriteFile(snap, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fakeMediaStore{refs: []storage.EventMediaRef{
		{ID: "e1", SnapshotPath: snap, SnapshotAvailable: true, ClipPath: "", ClipAvailable: false},
	}}

	ReconcileEventMediaAvailability(store)

	if len(store.writes) != 0 {
		t.Errorf("expected no writes when availability is unchanged, got %v", store.writes)
	}
}

// TestReconcile_WritesWhenSnapshotMissing verifies a single combined write when
// a snapshot file has been deleted out from under a still-available row.
func TestReconcile_WritesWhenSnapshotMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone.jpg")

	store := &fakeMediaStore{refs: []storage.EventMediaRef{
		{ID: "e1", SnapshotPath: missing, SnapshotAvailable: true, ClipPath: "", ClipAvailable: false},
	}}

	ReconcileEventMediaAvailability(store)

	want := []mediaWrite{{id: "e1", snap: false, clip: false}}
	if len(store.writes) != 1 || store.writes[0] != want[0] {
		t.Errorf("writes = %v, want %v", store.writes, want)
	}
}

// TestReconcile_WritesWhenClipMissing verifies the clip flag is corrected while
// a present snapshot stays available, in one combined write.
func TestReconcile_WritesWhenClipMissing(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "snap.jpg")
	if err := os.WriteFile(snap, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingClip := filepath.Join(dir, "gone.mp4")

	store := &fakeMediaStore{refs: []storage.EventMediaRef{
		{ID: "e1", SnapshotPath: snap, SnapshotAvailable: true, ClipPath: missingClip, ClipAvailable: true},
	}}

	ReconcileEventMediaAvailability(store)

	want := mediaWrite{id: "e1", snap: true, clip: false}
	if len(store.writes) != 1 || store.writes[0] != want {
		t.Errorf("writes = %v, want %v", store.writes, want)
	}
}

// TestReconcile_SkipsAlreadyUnavailable verifies a row already flagged
// unavailable for a missing file is not rewritten.
func TestReconcile_SkipsAlreadyUnavailable(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone.jpg")

	store := &fakeMediaStore{refs: []storage.EventMediaRef{
		{ID: "e1", SnapshotPath: missing, SnapshotAvailable: false, ClipPath: "", ClipAvailable: false},
	}}

	ReconcileEventMediaAvailability(store)

	if len(store.writes) != 0 {
		t.Errorf("expected no writes for an already-unavailable row, got %v", store.writes)
	}
}
