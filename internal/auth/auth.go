package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxFailures         = 10
	maxTokenCreations   = 20
	failureWindow       = 5 * time.Minute
	cleanupInterval     = time.Minute
	SessionCookieName   = "vedetta_session"
	CSRFCookieName      = "vedetta_csrf"
	SessionAbsoluteTTL  = 12 * time.Hour
	SessionIdleTTL      = 30 * time.Minute
	RememberAbsoluteTTL = 30 * 24 * time.Hour // 30 days
	RememberIdleTTL     = 7 * 24 * time.Hour  // 7 days
)

const (
	AuthKindSession = "session"
	AuthKindToken   = "token"
	AuthKindProxy   = "proxy"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInsufficientScope  = errors.New("insufficient scope")
	ErrRateLimited        = errors.New("rate limited")
	ErrStorageUnavailable = errors.New("auth storage unavailable")
)

type failureRecord struct {
	count   int
	firstAt time.Time
}

type Principal struct {
	Username  string    `json:"username"`
	Kind      string    `json:"kind"`
	Scopes    []string  `json:"scopes,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	SessionID string    `json:"-"`
	CSRFToken string    `json:"-"`
	TokenID   int64     `json:"-"`
}

type Checker struct {
	users          map[string][]byte
	dummyHash      []byte
	db             *storage.DB
	exposure       string
	trustedProxies []netip.Prefix
	proxyHeader    string

	mu            sync.Mutex
	loginFailures map[string]*failureRecord
	tokenCreates  map[string]*failureRecord
	done          chan struct{}
}

func New(authCfg config.AuthConfig, apiCfg config.APIConfig, db *storage.DB) *Checker {
	if len(authCfg.Users) == 0 {
		return nil
	}

	c := &Checker{
		users:          make(map[string][]byte, len(authCfg.Users)),
		db:             db,
		exposure:       apiCfg.Exposure,
		loginFailures:  make(map[string]*failureRecord),
		tokenCreates:   make(map[string]*failureRecord),
		done:           make(chan struct{}),
		trustedProxies: parseTrustedProxies(apiCfg.TrustedProxies),
		proxyHeader:    authCfg.Proxy.Header,
	}

	for _, user := range authCfg.Users {
		c.users[user.Username] = []byte(user.PasswordHash)
	}
	c.dummyHash = makeDummyHash(c.users)

	go c.cleanupLoop()
	slog.Info("authentication enabled", "users", len(c.users))
	return c
}

// NewFromDB creates a Checker that reads users from the database rather than
// from static config. It loads users immediately and supports reloading via
// reloadUsers.
func NewFromDB(authCfg config.AuthConfig, apiCfg config.APIConfig, db *storage.DB) *Checker {
	c := &Checker{
		users:          make(map[string][]byte),
		db:             db,
		exposure:       apiCfg.Exposure,
		loginFailures:  make(map[string]*failureRecord),
		tokenCreates:   make(map[string]*failureRecord),
		done:           make(chan struct{}),
		trustedProxies: parseTrustedProxies(apiCfg.TrustedProxies),
		proxyHeader:    authCfg.Proxy.Header,
	}

	c.reloadUsers()
	c.dummyHash = makeDummyHash(c.users)

	go c.cleanupLoop()
	slog.Info("authentication enabled (db-primary)", "users", len(c.users))
	return c
}

// reloadUsers fetches all auth users from the database and replaces the
// in-memory user map. Safe for concurrent use.
func (c *Checker) reloadUsers() {
	if c.db == nil {
		return
	}

	dbUsers, err := c.db.ListAuthUsers()
	if err != nil {
		slog.Error("failed to reload auth users from database", "error", err)
		return
	}

	// Build the replacement map and dummy hash before locking: makeDummyHash
	// runs bcrypt, and verify() holds c.mu, so doing this work under the lock
	// would stall all concurrent logins. Lock only to swap the fields.
	users := make(map[string][]byte, len(dbUsers))
	for _, u := range dbUsers {
		users[u.Username] = []byte(u.PasswordHash)
	}
	dummy := makeDummyHash(users)

	c.mu.Lock()
	c.users = users
	c.dummyHash = dummy
	c.mu.Unlock()
}

func ValidateConfig(cfg config.AuthConfig) error {
	if len(cfg.Users) == 0 {
		return fmt.Errorf("auth: at least one user must be configured")
	}

	seen := make(map[string]struct{}, len(cfg.Users))
	for i, user := range cfg.Users {
		if user.Username == "" {
			return fmt.Errorf("auth.users[%d]: username is required", i)
		}
		if _, ok := seen[user.Username]; ok {
			return fmt.Errorf("auth.users[%d]: duplicate username %q", i, user.Username)
		}
		seen[user.Username] = struct{}{}
		if user.PasswordHash == "" {
			return fmt.Errorf("auth.users[%d]: password_hash is required", i)
		}
		err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("probe"))
		if err != nil && err != bcrypt.ErrMismatchedHashAndPassword {
			return fmt.Errorf("auth.users[%d]: invalid bcrypt hash: %w", i, err)
		}
	}

	return nil
}

func (c *Checker) Close() {
	if c == nil {
		return
	}
	close(c.done)
}

func (c *Checker) Check(user, pass, remoteIP string) bool {
	if c == nil {
		return true
	}
	if c.isRateLimited(c.loginFailures, remoteIP, maxFailures) {
		slog.Warn("auth rate limited", "ip", remoteIP)
		return false
	}
	if !c.verify(user, pass) {
		c.recordFailure(c.loginFailures, remoteIP)
		slog.Warn("auth failed", "ip", remoteIP, "username", user)
		return false
	}
	c.clearFailures(c.loginFailures, remoteIP)
	return true
}

// ProxyAuthEnabled reports whether proxy authentication is configured.
func (c *Checker) ProxyAuthEnabled() bool {
	return c.proxyHeader != ""
}

// Enabled reports whether authentication is active. A nil Checker means no
// auth users were configured, so the API and RTSP republish are open. This
// matches the gate used by the RTSP republish server (auth == nil => open).
func (c *Checker) Enabled() bool {
	return c != nil
}

// UpdatePassword updates the in-memory password hash for a user.
func (c *Checker) UpdatePassword(username string, hash []byte) {
	// Compute the dummy hash from the new hash before locking so the bcrypt
	// work doesn't stall logins waiting on c.mu. The new hash is representative
	// of the user set's cost.
	dummy := makeDummyHash(map[string][]byte{username: hash})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.users[username] = hash
	c.dummyHash = dummy
}

// ChangePassword verifies the current password and updates to the new one.
func (c *Checker) ChangePassword(username, currentPassword, newPassword string) error {
	if !c.verify(username, currentPassword) {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := c.db.SaveAuthUser(username, string(hash)); err != nil {
		return fmt.Errorf("save user: %w", err)
	}
	c.reloadUsers()
	slog.Info("password changed", "username", username)
	return nil
}

func (c *Checker) Login(user, pass, remoteIP, userAgent string, remember bool) (*storage.AuthSession, error) {
	if c == nil {
		return nil, ErrInvalidCredentials
	}
	if c.db == nil {
		return nil, ErrStorageUnavailable
	}
	if c.isRateLimited(c.loginFailures, remoteIP, maxFailures) {
		slog.Warn("login rate limited", "ip", remoteIP, "username", user)
		return nil, ErrRateLimited
	}
	if !c.verify(user, pass) {
		c.recordFailure(c.loginFailures, remoteIP)
		slog.Warn("login failed", "ip", remoteIP, "username", user)
		return nil, ErrInvalidCredentials
	}
	c.clearFailures(c.loginFailures, remoteIP)

	sessionID, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}
	csrfToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	absoluteTTL := SessionAbsoluteTTL
	idleTTL := SessionIdleTTL
	if remember {
		absoluteTTL = RememberAbsoluteTTL
		idleTTL = RememberIdleTTL
	}

	now := time.Now().UTC()
	session := &storage.AuthSession{
		ID:         sessionID,
		Username:   user,
		CSRFToken:  csrfToken,
		RemoteIP:   remoteIP,
		UserAgent:  userAgent,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(absoluteTTL),
		IdleTTL:    idleTTL,
	}
	if err := c.db.CreateSession(*session); err != nil {
		return nil, err
	}

	slog.Info("login succeeded", "username", user, "ip", remoteIP, "remember", remember)
	return session, nil
}

func (c *Checker) Logout(sessionID, username string) error {
	if c == nil || c.db == nil || sessionID == "" {
		return nil
	}
	if err := c.db.DeleteSession(sessionID); err != nil {
		return err
	}
	slog.Info("logout", "username", username, "session_id", sessionID)
	return nil
}

func (c *Checker) Authenticate(r *http.Request) (*Principal, error) {
	if c == nil {
		return nil, nil
	}
	if p := c.authenticateProxyHeader(r); p != nil {
		return p, nil
	}
	if p, err := c.authenticateBearerToken(r); p != nil || err != nil {
		return p, err
	}
	return c.authenticateSession(r)
}

func (c *Checker) authenticateProxyHeader(r *http.Request) *Principal {
	if c.proxyHeader == "" {
		return nil
	}
	if !c.isTrustedProxy(remoteAddrIP(r.RemoteAddr)) {
		return nil
	}
	username := strings.TrimSpace(r.Header.Get(c.proxyHeader))
	if username == "" {
		return nil
	}
	slog.Debug("proxy auth", "username", username, "header", c.proxyHeader)
	return &Principal{
		Username: username,
		Kind:     AuthKindProxy,
		Scopes:   []string{"*"},
	}
}

func (c *Checker) authenticateSession(r *http.Request) (*Principal, error) {
	if c.db == nil {
		return nil, ErrStorageUnavailable
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, nil
	}

	session, err := c.db.GetSession(cookie.Value)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	now := time.Now().UTC()
	idleTTL := session.IdleTTL
	if idleTTL <= 0 {
		idleTTL = SessionIdleTTL
	}
	if now.After(session.ExpiresAt) || now.Sub(session.LastSeenAt) > idleTTL {
		_ = c.db.DeleteSession(session.ID)
		return nil, nil
	}
	if err := c.db.TouchSession(session.ID, now); err != nil {
		return nil, err
	}

	return &Principal{
		Username:  session.Username,
		Kind:      AuthKindSession,
		Scopes:    []string{"*"},
		ExpiresAt: session.ExpiresAt,
		SessionID: session.ID,
		CSRFToken: session.CSRFToken,
	}, nil
}

func (c *Checker) authenticateBearerToken(r *http.Request) (*Principal, error) {
	if c.db == nil {
		return nil, ErrStorageUnavailable
	}

	authz := r.Header.Get("Authorization")
	if authz == "" || !strings.HasPrefix(authz, "Bearer ") {
		return nil, nil
	}
	raw := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if raw == "" {
		return nil, nil
	}

	hash := sha256.Sum256([]byte(raw))
	token, err := c.db.GetAPITokenByHash(hash[:])
	if err != nil {
		return nil, err
	}
	if token == nil || !token.RevokedAt.IsZero() {
		return nil, nil
	}

	now := time.Now().UTC()
	if err := c.db.TouchAPIToken(token.ID, now); err != nil {
		return nil, err
	}

	return &Principal{
		Username: token.Username,
		Kind:     AuthKindToken,
		Scopes:   append([]string(nil), token.Scopes...),
		TokenID:  token.ID,
	}, nil
}

func (c *Checker) RequireCSRF(r *http.Request, p *Principal) bool {
	if p == nil || p.Kind != AuthKindSession || isSafeMethod(r.Method) {
		return true
	}

	csrfCookie, err := r.Cookie(CSRFCookieName)
	if err != nil || csrfCookie.Value == "" {
		return false
	}
	header := r.Header.Get("X-CSRF-Token")
	if header == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(header), []byte(csrfCookie.Value)) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(p.CSRFToken)) == 1
}

func (c *Checker) CreateToken(username, name string, scopes []string, remoteIP string) (*storage.APIToken, string, error) {
	normalized, err := normalizeTokenScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	return c.createToken(username, name, normalized, remoteIP)
}

func (c *Checker) CreateTokenForPrincipal(principal *Principal, name string, scopes []string, remoteIP string) (*storage.APIToken, string, error) {
	if principal == nil {
		return nil, "", ErrInvalidCredentials
	}
	normalized, err := normalizeTokenScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	if principal.Kind == AuthKindToken {
		for _, scope := range normalized {
			if !principal.HasAnyScope(scope) {
				return nil, "", ErrInsufficientScope
			}
		}
	}
	return c.createToken(principal.Username, name, normalized, remoteIP)
}

func (c *Checker) createToken(username, name string, scopes []string, remoteIP string) (*storage.APIToken, string, error) {
	if c == nil {
		return nil, "", ErrInvalidCredentials
	}
	if c.db == nil {
		return nil, "", ErrStorageUnavailable
	}
	if name == "" {
		return nil, "", fmt.Errorf("token name is required")
	}

	rateKey := username + "|" + remoteIP
	if c.isRateLimited(c.tokenCreates, rateKey, maxTokenCreations) {
		slog.Warn("token creation rate limited", "username", username, "ip", remoteIP)
		return nil, "", ErrRateLimited
	}
	c.recordFailure(c.tokenCreates, rateKey)

	rawToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256([]byte(rawToken))
	now := time.Now().UTC()

	token := &storage.APIToken{
		Username:    username,
		Name:        name,
		TokenPrefix: rawToken[:12],
		TokenHash:   hash[:],
		Scopes:      append([]string(nil), scopes...),
		CreatedAt:   now,
	}
	id, err := c.db.CreateAPIToken(*token)
	if err != nil {
		return nil, "", err
	}
	token.ID = id

	slog.Info("api token created", "username", username, "token_id", id, "name", name)
	return token, rawToken, nil
}

func normalizeTokenScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{"api:read"}, nil
	}
	normalized := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return nil, fmt.Errorf("token scope cannot be empty")
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("token scope cannot be empty")
	}
	return normalized, nil
}

func (c *Checker) RevokeToken(id int64, username string) error {
	if c == nil || c.db == nil {
		return ErrStorageUnavailable
	}
	if err := c.db.RevokeAPIToken(id, username); err != nil {
		return err
	}
	slog.Info("api token revoked", "username", username, "token_id", id)
	return nil
}

func (c *Checker) SetSessionCookies(w http.ResponseWriter, r *http.Request, session *storage.AuthSession) {
	if session == nil {
		return
	}
	secure := c.cookieSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  session.ExpiresAt,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    session.CSRFToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  session.ExpiresAt,
	})
}

func (c *Checker) ClearSessionCookies(w http.ResponseWriter, r *http.Request) {
	expired := time.Unix(0, 0).UTC()
	secure := c.cookieSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expired,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expired,
		MaxAge:   -1,
	})
}

func (c *Checker) ClientIP(r *http.Request) string {
	direct := remoteAddrIP(r.RemoteAddr)
	if direct == "" {
		return ""
	}
	if c.isTrustedProxy(direct) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if addr, err := netip.ParseAddr(first); err == nil {
				return addr.String()
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			if addr, err := netip.ParseAddr(realIP); err == nil {
				return addr.String()
			}
		}
	}
	return direct
}

func (c *Checker) RequestIsSecure(r *http.Request) bool {
	if c == nil || c.exposure != "internet" {
		return true
	}
	return c.cookieSecure(r)
}

// makeDummyHash builds the constant fake hash that verify() compares against
// for unknown usernames. Its bcrypt cost matches an existing user hash so the
// unknown-user comparison takes the same time as a real one; a cheaper dummy
// would make the unknown-user path measurably faster and leak whether a
// username exists. Falls back to DefaultCost when no user hash cost is readable.
func makeDummyHash(users map[string][]byte) []byte {
	cost := bcrypt.DefaultCost
	for _, h := range users {
		if k, err := bcrypt.Cost(h); err == nil {
			cost = k
			break
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("vedetta-not-a-real-password"), cost)
	if err != nil {
		panic(err)
	}
	return hash
}

func (c *Checker) verify(user, pass string) bool {
	// Snapshot the hash and dummy under the lock, then run the expensive bcrypt
	// compare without holding it: reloadUsers/UpdatePassword replace these
	// fields (never mutate in place), so a copied reference stays valid, and we
	// avoid serializing every login behind one another's bcrypt cost.
	c.mu.Lock()
	hash, ok := c.users[user]
	dummy := c.dummyHash
	c.mu.Unlock()
	if !ok {
		_ = bcrypt.CompareHashAndPassword(dummy, []byte(pass))
		return false
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(pass)) == nil
}

func (c *Checker) cookieSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if !c.isTrustedProxy(remoteAddrIP(r.RemoteAddr)) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (c *Checker) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.expireFailureMap(c.loginFailures)
			c.expireFailureMap(c.tokenCreates)
			if c.db != nil {
				if err := c.db.DeleteExpiredSessions(time.Now().UTC()); err != nil {
					slog.Warn("failed to delete expired sessions", "error", err)
				}
			}
		}
	}
}

func (c *Checker) expireFailureMap(m map[string]*failureRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, rec := range m {
		if now.Sub(rec.firstAt) > failureWindow {
			delete(m, key)
		}
	}
}

func (c *Checker) isRateLimited(m map[string]*failureRecord, key string, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	rec, ok := m[key]
	if !ok {
		return false
	}
	if time.Since(rec.firstAt) > failureWindow {
		delete(m, key)
		return false
	}
	return rec.count >= limit
}

func (c *Checker) recordFailure(m map[string]*failureRecord, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	rec, ok := m[key]
	if !ok || time.Since(rec.firstAt) > failureWindow {
		m[key] = &failureRecord{count: 1, firstAt: time.Now()}
		return
	}
	rec.count++
}

func (c *Checker) clearFailures(m map[string]*failureRecord, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(m, key)
}

func (c *Checker) isTrustedProxy(raw string) bool {
	if raw == "" {
		return false
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	for _, prefix := range c.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (p *Principal) HasAnyScope(scopes ...string) bool {
	if p == nil {
		return false
	}
	if p.Kind == AuthKindSession || p.Kind == AuthKindProxy {
		return true
	}
	for _, want := range scopes {
		for _, have := range p.Scopes {
			if scopeMatches(have, want) {
				return true
			}
		}
	}
	return false
}

func (p *Principal) Allows(method, path string) bool {
	if p == nil {
		return false
	}
	if p.Kind == AuthKindSession || p.Kind == AuthKindProxy {
		return true
	}
	if !strings.HasPrefix(path, "/api/") && path != "/metrics" {
		return false
	}
	if path == "/api/tokens" || strings.HasPrefix(path, "/api/tokens/") {
		if isSafeMethod(method) {
			return p.HasAnyScope("tokens:read", "tokens:write", "api:*", "*")
		}
		return p.HasAnyScope("tokens:write", "api:*", "*")
	}
	if isSafeMethod(method) {
		return p.HasAnyScope("api:read", "api:*", "*")
	}
	return p.HasAnyScope("api:write", "api:*", "*")
}

func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func scopeMatches(have, want string) bool {
	if have == "*" || have == want {
		return true
	}
	if strings.HasSuffix(have, "*") {
		return strings.HasPrefix(want, strings.TrimSuffix(have, "*"))
	}
	return false
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func generateOpaqueToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func remoteAddrIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return ""
	}
	return addr.Unmap().String()
}

func parseTrustedProxies(values []string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil {
			result = append(result, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(value); err == nil {
			result = append(result, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return result
}
