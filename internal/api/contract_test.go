package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

type contractValidator struct {
	router routers.Router
	t      *testing.T
}

func newContractValidator(t *testing.T) *contractValidator {
	t.Helper()

	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("validate openapi spec: %v", err)
	}

	// Required for path matching — the router needs a server URL to strip prefixes
	spec.Servers = openapi3.Servers{{URL: "/"}}

	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		t.Fatalf("create openapi router: %v", err)
	}

	return &contractValidator{router: router, t: t}
}

func (cv *contractValidator) validate(req *http.Request, rec *httptest.ResponseRecorder) {
	cv.t.Helper()

	route, pathParams, err := cv.router.FindRoute(req)
	if err != nil {
		cv.t.Fatalf("find route for %s %s: %v", req.Method, req.URL.Path, err)
	}

	reqInput := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			// Skip auth validation in contract tests
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
	}

	body := rec.Body.Bytes()
	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(body)),
	}

	if err := openapi3filter.ValidateResponse(context.Background(), respInput); err != nil {
		cv.t.Errorf("response validation failed for %s %s (status %d):\n%s\nBody: %s",
			req.Method, req.URL.Path, rec.Code, err, string(body))
	}
}

func TestContract_GetHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetHealthLive(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health/live", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}

func TestContract_GetHealthReady(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health/ready", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	// Ready endpoint may return 200 or 503 depending on state
	cv.validate(req, rec)
}

func TestContract_GetSystem(t *testing.T) {
	srv, _ := newTestServer(t)
	cv := newContractValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cv.validate(req, rec)
}
