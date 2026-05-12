package recording

import (
	"fmt"
	"image"

	"github.com/rvben/vedetta/internal/camera"
)

// SaveEventSnapshot writes img to primaryPath via the recorder's snapshot
// saver (falling back to local disk on ENOSPC), persists the resolved path
// on the event row, and sets snapshot_available to true.
//
// The caller MUST have already inserted the event row via db.SaveEvent before
// calling SaveEventSnapshot.
//
// If the file write fails the error is returned immediately and the DB row is
// not updated. If the write succeeds but a DB update fails, the resolved path
// is returned alongside the error so the caller can decide whether to clean up
// the on-disk file.
//
// SaveEventSnapshot acquires segmentOpMu so that a concurrent retention or
// emergency-delete pass cannot remove the snapshot directory between the write
// and the DB update.
func (r *Recorder) SaveEventSnapshot(event camera.Event, img *image.RGBA, primaryPath string) (string, error) {
	r.segmentOpMu.Lock()
	defer r.segmentOpMu.Unlock()

	if r.snapshotSaver == nil {
		return "", fmt.Errorf("recorder: snapshot saver not configured")
	}

	resolved, err := r.snapshotSaver.Save(img, primaryPath)
	if err != nil {
		return "", fmt.Errorf("save snapshot: %w", err)
	}

	if err := r.db.UpdateEventSnapshotPath(event.ID, resolved); err != nil {
		return resolved, fmt.Errorf("persist snapshot path: %w", err)
	}
	if err := r.db.UpdateEventSnapshotAvailability(event.ID, true); err != nil {
		return resolved, fmt.Errorf("persist snapshot availability: %w", err)
	}
	return resolved, nil
}
