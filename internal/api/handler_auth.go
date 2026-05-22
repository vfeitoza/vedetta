package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
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
		s.serverError(w, r, err)
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

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil || principal.Kind != auth.AuthKindSession {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session authentication required"})
		return
	}
	if err := s.auth.Logout(principal.SessionID, principal.Username); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.auth.ClearSessionCookies(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) GetAuthMe(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) CreateToken(w http.ResponseWriter, r *http.Request) {
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

	token, rawToken, err := s.auth.CreateTokenForPrincipal(principal, req.Name, req.Scopes, s.auth.ClientIP(r))
	switch err {
	case nil:
	case auth.ErrRateLimited:
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
		return
	case auth.ErrInsufficientScope:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient scope"})
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

func (s *Server) DeleteToken(w http.ResponseWriter, r *http.Request, id int64) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := s.auth.RevokeToken(id, principal.Username); err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) ListTokens(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tokens, err := s.db.ListAPITokensByUser(principal.Username)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	type tokenItem struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		TokenPrefix string   `json:"token_prefix"`
		Scopes      []string `json:"scopes"`
		CreatedAt   string   `json:"created_at"`
		LastUsedAt  string   `json:"last_used_at,omitempty"`
	}
	items := make([]tokenItem, 0, len(tokens))
	for _, t := range tokens {
		item := tokenItem{
			ID:          t.ID,
			Name:        t.Name,
			TokenPrefix: t.TokenPrefix,
			Scopes:      t.Scopes,
			CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		}
		if !t.LastUsedAt.IsZero() {
			item.LastUsedAt = t.LastUsedAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	principal := principalFromContext(r.Context())
	if principal == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password is required"})
		return
	}
	if len(req.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must be at least 8 characters"})
		return
	}

	// Verify current password (skip for proxy-auth users setting initial local password)
	if principal.Kind != auth.AuthKindProxy {
		if s.auth == nil || !s.auth.Check(principal.Username, req.CurrentPassword, s.auth.ClientIP(r)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "current password is incorrect"})
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("bcrypt hash failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	newHash := string(hash)

	if err := s.db.SaveAuthUser(principal.Username, newHash); err != nil {
		slog.Error("failed to save auth user", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save user"})
		return
	}

	if err := config.UpdateAuthPassword(s.configPath, principal.Username, newHash); err != nil {
		slog.Warn("failed to update password in config file (DB updated)", "error", err)
	}

	if s.auth != nil {
		s.auth.UpdatePassword(principal.Username, hash)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
