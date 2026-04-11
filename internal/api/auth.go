package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/rvben/vedetta/internal/auth"
)

type principalContextKey struct{}

func authMiddleware(s *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w)

		if s.auth != nil && !s.auth.RequestIsSecure(r) {
			writeJSON(w, http.StatusUpgradeRequired, map[string]string{"error": "https required"})
			return
		}

		if isPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}

		if s.auth == nil {
			next.ServeHTTP(w, r)
			return
		}

		principal, err := s.auth.Authenticate(r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		// Login page: redirect if already authenticated, serve if not.
		if r.URL.Path == "/login.html" {
			if principal != nil {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if principal == nil {
			handleUnauthorized(w, r)
			return
		}
		if !principal.Allows(r.Method, r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient scope"})
			return
		}
		if !s.auth.RequireCSRF(r, principal) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf validation failed"})
			return
		}

		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}

func applySecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy())
}

func isPublicPath(r *http.Request) bool {
	switch {
	case r.URL.Path == "/favicon.svg":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		return true
	case r.Method == http.MethodGet && (r.URL.Path == "/api/health/live" || r.URL.Path == "/api/health/ready"):
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/openapi.json":
		return true
	// PWA assets that browsers must be able to fetch without a prior session:
	//   - manifest.webmanifest: read by Safari during Add to Home Screen before any login.
	//   - sw.js: navigator.serviceWorker.register rejects the SW on any redirect.
	//   - icon-*.png / badge-72.png: referenced from the manifest and from <head>
	//     apple-touch-icon; iOS fetches them without cookies in some paths.
	// None of these contain secrets; serving them anonymously is safe.
	case r.Method == http.MethodGet && r.URL.Path == "/manifest.webmanifest":
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/sw.js":
		return true
	case r.Method == http.MethodGet && isPWAIconPath(r.URL.Path):
		return true
	// Signed push-notification snapshot URLs. iOS fetches these without
	// session cookies when rendering notification thumbnails; the handler
	// itself enforces an HMAC signature check.
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/push/snapshot/"):
		return true
	default:
		return false
	}
}

// isPWAIconPath reports whether the path is one of the PWA icon assets that
// must be fetchable without authentication. Keep this list in sync with
// internal/api/static/manifest.webmanifest and the apple-touch-icon link tags
// in the HTML pages.
func isPWAIconPath(path string) bool {
	switch path {
	case "/icon-180.png", "/icon-192.png", "/icon-512.png", "/icon-512-maskable.png", "/badge-72.png":
		return true
	}
	return false
}

func handleUnauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/metrics" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	next := r.URL.RequestURI()
	http.Redirect(w, r, "/login.html?next="+url.QueryEscape(next), http.StatusFound)
}

func principalFromContext(ctx context.Context) *auth.Principal {
	principal, _ := ctx.Value(principalContextKey{}).(*auth.Principal)
	return principal
}
