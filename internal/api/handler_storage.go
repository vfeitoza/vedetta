package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/rvben/vedetta/internal/recording"
)

// GetStorage returns the cached storage breakdown for all cameras.
func (s *Server) GetStorage(w http.ResponseWriter, r *http.Request) {
	out, err := s.recorder.StorageBreakdown()
	if err != nil {
		s.serverError(w, r, err)
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

// PostStorageCleanup triggers an async retention-cleanup pass. Returns 200 {"started":true}
// immediately, or 409 if a cleanup is already running.
func (s *Server) PostStorageCleanup(w http.ResponseWriter, r *http.Request) {
	if err := s.recorder.TryRunCleanupAsync(); errors.Is(err, recording.ErrStorageBusy) {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "storage busy"})
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": true})
}

// GetStorageAudit returns the most recent storage-audit entries (default 50, max 500).
func (s *Server) GetStorageAudit(w http.ResponseWriter, r *http.Request, params GetStorageAuditParams) {
	limit := 50
	if params.Limit != nil && *params.Limit > 0 && *params.Limit <= 500 {
		limit = *params.Limit
	}
	entries, err := s.recorder.StorageAudit(limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	type auditRow struct {
		TS    time.Time      `json:"ts"`
		Actor string         `json:"actor"`
		Scope map[string]any `json:"scope"`
		Bytes int64          `json:"bytes"`
		Files int            `json:"files"`
	}
	resp := make([]auditRow, 0, len(entries))
	for _, e := range entries {
		var scope map[string]any
		_ = json.Unmarshal([]byte(e.ScopeJSON), &scope)
		resp = append(resp, auditRow{e.Timestamp, e.Actor, scope, e.Bytes, e.Files})
	}
	writeJSON(w, http.StatusOK, resp)
}
