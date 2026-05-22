package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serverError must log the underlying error server-side but return a generic
// message to the client so internal details (raw SQLite errors, file paths,
// schema names) are never leaked in the HTTP response body.
func TestServerErrorDoesNotLeakDetails(t *testing.T) {
	srv, _ := newTestServer(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/secret", nil)
	srv.serverError(w, r, errors.New("SQL logic error: no such column secret_table.password"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret_table") || strings.Contains(body, "SQL logic error") {
		t.Fatalf("response leaked internal error detail: %s", body)
	}
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("response missing generic error message: %s", body)
	}
}

// serverError must tolerate a nil request (some call sites are in closures
// without a request in scope).
func TestServerErrorNilRequest(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	srv.serverError(w, nil, errors.New("boom"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// serverErrorMsg lets a handler surface a meaningful, non-sensitive client
// message (e.g. "embedding failed") while still keeping the underlying error
// detail out of the response body and only in the server log.
func TestServerErrorMsgUsesClientMessageWithoutLeak(t *testing.T) {
	srv, _ := newTestServer(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/objects/identify", nil)
	srv.serverErrorMsg(w, r, errors.New("SQL logic error: no such column secret_table.password"), "embedding failed")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret_table") || strings.Contains(body, "SQL logic error") {
		t.Fatalf("response leaked internal error detail: %s", body)
	}
	if !strings.Contains(body, "embedding failed") {
		t.Fatalf("response missing client message: %s", body)
	}
}

// serverErrorText is the text/plain analogue used by HTML partial handlers,
// which write with http.Error rather than JSON. It must not leak raw error
// detail into the response body.
func TestServerErrorTextDoesNotLeakDetails(t *testing.T) {
	srv, _ := newTestServer(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/partials/events-gallery", nil)
	srv.serverErrorText(w, r, errors.New("SQL logic error: no such column secret_table.password"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret_table") || strings.Contains(body, "SQL logic error") {
		t.Fatalf("response leaked internal error detail: %s", body)
	}
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("response missing generic error message: %s", body)
	}
}
