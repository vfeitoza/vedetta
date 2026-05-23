package recording

import (
	"log/slog"
	"os"

	"github.com/rvben/vedetta/internal/storage"
)

// eventMediaStore is the slice of storage the reconciler needs. Injecting it as
// an interface keeps the skip-when-unchanged logic unit-testable; *storage.DB
// satisfies it in production.
type eventMediaStore interface {
	EventMediaRefs() ([]storage.EventMediaRef, error)
	UpdateEventMediaAvailability(id string, snapshotAvailable, clipAvailable bool) error
}

// ReconcileEventMediaAvailability brings each event's snapshot/clip availability
// flags back in sync with the filesystem. It stats the referenced files and
// writes a single combined update ONLY when the computed availability differs
// from what is already stored, so a steady state (files present) issues no
// writes at all.
func ReconcileEventMediaAvailability(store eventMediaStore) {
	refs, err := store.EventMediaRefs()
	if err != nil {
		slog.Error("failed to query events for media reconciliation", "error", err)
		return
	}

	for _, ref := range refs {
		snapshotAvailable := ref.SnapshotPath != "" && fileExists(ref.SnapshotPath)
		clipAvailable := ref.ClipPath != "" && fileExists(ref.ClipPath)

		if snapshotAvailable == ref.SnapshotAvailable && clipAvailable == ref.ClipAvailable {
			continue // nothing changed; avoid a needless row write
		}

		if err := store.UpdateEventMediaAvailability(ref.ID, snapshotAvailable, clipAvailable); err != nil {
			slog.Error("failed to update media availability", "id", ref.ID, "error", err)
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
