package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

const defaultAPIBodyLimit int64 = 1 << 20

func apiBodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldLimitAPIBody(r) {
			next.ServeHTTP(w, r)
			return
		}

		limit := requestBodyLimit(r)
		if r.ContentLength > limit {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}

		defer r.Body.Close()
		data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
			return
		}
		if int64(len(data)) > limit {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(data))
		r.ContentLength = int64(len(data))
		next.ServeHTTP(w, r)
	})
}

func shouldLimitAPIBody(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/")
}

func requestBodyLimit(r *http.Request) int64 {
	return defaultAPIBodyLimit
}
