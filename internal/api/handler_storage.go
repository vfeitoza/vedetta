package api

import (
	"net/http"
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

// PostStorageDelete is a stub; full implementation follows in Task 23.
func (s *Server) PostStorageDelete(w http.ResponseWriter, _ *http.Request, _ PostStorageDeleteParams) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// PostStorageCleanup is a stub; full implementation follows in Task 24.
func (s *Server) PostStorageCleanup(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// GetStorageAudit is a stub; full implementation follows in Task 24.
func (s *Server) GetStorageAudit(w http.ResponseWriter, _ *http.Request, _ GetStorageAuditParams) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
