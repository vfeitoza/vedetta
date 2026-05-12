package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rvben/vedetta/internal/recording"
)

// GetStorage returns the cached storage breakdown for all cameras.
func (s *Server) GetStorage(w http.ResponseWriter, _ *http.Request) {
	out, err := s.recorder.StorageBreakdown()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PostStorageDelete validates the request, delegates to recorder.DeleteStorage,
// maps recording-layer errors to HTTP statuses, and writes an audit row on
// successful non-dry-run deletions.
func (s *Server) PostStorageDelete(w http.ResponseWriter, r *http.Request, params PostStorageDeleteParams) {
	var req recording.DeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if params.DryRun != nil {
		req.DryRun = *params.DryRun
	}

	result, err := s.recorder.DeleteStorage(req)
	if errors.Is(err, recording.ErrStorageBusy) {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "storage busy"})
		return
	}
	var protectedErr *recording.ErrOpenSegmentProtected
	if errors.As(err, &protectedErr) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":           "request targets currently-open segment(s)",
			"protected_paths": protectedErr.Paths,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if !req.DryRun {
		actor := storageActor(r)
		_ = s.recorder.RecordStorageAudit(
			actor,
			req.MarshalScope(),
			result.Bytes,
			result.Segments+result.Clips+result.Snapshots,
		)
	}
	writeJSON(w, http.StatusOK, result)
}

// storageActor returns the username of the authenticated principal, or "local"
// when the request arrives without an authenticated session (e.g. internal calls).
func storageActor(r *http.Request) string {
	p := principalFromContext(r.Context())
	if p == nil {
		return "local"
	}
	return p.Username
}

// PostStorageCleanup is a stub; full implementation follows in Task 24.
func (s *Server) PostStorageCleanup(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// GetStorageAudit is a stub; full implementation follows in Task 24.
func (s *Server) GetStorageAudit(w http.ResponseWriter, _ *http.Request, _ GetStorageAuditParams) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
