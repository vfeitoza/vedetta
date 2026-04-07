package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
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

func applySecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; object-src 'none'; base-uri 'self'")
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
	default:
		return false
	}
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authentication unavailable"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	session, err := s.auth.Login(req.Username, req.Password, s.auth.ClientIP(r), r.UserAgent(), req.Remember)
	switch err {
	case nil:
	case auth.ErrRateLimited:
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
		return
	case auth.ErrInvalidCredentials:
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.auth.SetSessionCookies(w, r, session)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   session.Username,
		"kind":       auth.AuthKindSession,
		"csrf_token": session.CSRFToken,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil || principal.Kind != auth.AuthKindSession {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session authentication required"})
		return
	}
	if err := s.auth.Logout(principal.SessionID, principal.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.auth.ClearSessionCookies(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	resp := map[string]any{
		"username": principal.Username,
		"kind":     principal.Kind,
		"scopes":   principal.Scopes,
	}
	if !principal.ExpiresAt.IsZero() {
		resp["expires_at"] = principal.ExpiresAt
		resp["csrf_token"] = principal.CSRFToken
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	token, rawToken, err := s.auth.CreateToken(principal.Username, req.Name, req.Scopes, s.auth.ClientIP(r))
	switch err {
	case nil:
	case auth.ErrRateLimited:
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           token.ID,
		"name":         token.Name,
		"token":        rawToken,
		"token_prefix": token.TokenPrefix,
		"scopes":       token.Scopes,
		"created_at":   token.CreatedAt,
	})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid token ID"})
		return
	}
	if err := s.auth.RevokeToken(id, principal.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current_password and new_password required"})
		return
	}
	if len(req.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must be at least 8 characters"})
		return
	}

	if err := s.auth.ChangePassword(principal.Username, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
