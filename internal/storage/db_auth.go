package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AuthSession struct {
	ID         string
	Username   string
	CSRFToken  string
	RemoteIP   string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IdleTTL    time.Duration
}

type APIToken struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	Name        string    `json:"name"`
	TokenPrefix string    `json:"token_prefix"`
	Scopes      []string  `json:"scopes"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	RevokedAt   time.Time `json:"revoked_at,omitempty"`
	TokenHash   []byte    `json:"-"`
}

func (d *DB) CreateSession(session AuthSession) error {
	idleSecs := int64(session.IdleTTL.Seconds())
	if idleSecs <= 0 {
		idleSecs = 1800 // default 30 minutes
	}
	_, err := d.db.Exec(`
		INSERT INTO auth_sessions (id, username, csrf_token, remote_ip, user_agent, created_at, last_seen_at, expires_at, idle_ttl_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.Username, session.CSRFToken, nullString(session.RemoteIP), nullString(session.UserAgent),
		utc(session.CreatedAt), utc(session.LastSeenAt), utc(session.ExpiresAt), idleSecs,
	)
	return err
}

func (d *DB) GetSession(id string) (*AuthSession, error) {
	row := d.db.QueryRow(`
		SELECT id, username, csrf_token, remote_ip, user_agent, created_at, last_seen_at, expires_at, idle_ttl_seconds
		FROM auth_sessions WHERE id = ?`, id)
	var session AuthSession
	var remoteIP, userAgent sql.NullString
	var idleSecs int64
	err := row.Scan(&session.ID, &session.Username, &session.CSRFToken, &remoteIP, &userAgent, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt, &idleSecs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session.RemoteIP = remoteIP.String
	session.UserAgent = userAgent.String
	session.IdleTTL = time.Duration(idleSecs) * time.Second
	return &session, nil
}

func (d *DB) TouchSession(id string, lastSeen time.Time) error {
	_, err := d.db.Exec("UPDATE auth_sessions SET last_seen_at = ? WHERE id = ?", utc(lastSeen), id)
	return err
}

func (d *DB) DeleteSession(id string) error {
	_, err := d.db.Exec("DELETE FROM auth_sessions WHERE id = ?", id)
	return err
}

func (d *DB) DeleteExpiredSessions(now time.Time) error {
	_, err := d.db.Exec(`DELETE FROM auth_sessions
		WHERE expires_at <= ?
		   OR (julianday(?) - julianday(last_seen_at)) * 86400 > idle_ttl_seconds`,
		utc(now), utc(now))
	return err
}

func (d *DB) CreateAPIToken(token APIToken) (int64, error) {
	scopesJSON, err := json.Marshal(token.Scopes)
	if err != nil {
		return 0, err
	}
	result, err := d.db.Exec(`
		INSERT INTO api_tokens (username, name, token_prefix, token_hash, scopes, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		token.Username, token.Name, token.TokenPrefix, token.TokenHash, string(scopesJSON), utc(token.CreatedAt),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *DB) GetAPITokenByHash(hash []byte) (*APIToken, error) {
	row := d.db.QueryRow(`
		SELECT id, username, name, token_prefix, token_hash, scopes, created_at, last_used_at, revoked_at
		FROM api_tokens WHERE token_hash = ?`, hash)
	var token APIToken
	var scopesJSON string
	var lastUsedAt, revokedAt sql.NullTime
	err := row.Scan(&token.ID, &token.Username, &token.Name, &token.TokenPrefix, &token.TokenHash, &scopesJSON, &token.CreatedAt, &lastUsedAt, &revokedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		token.LastUsedAt = lastUsedAt.Time
	}
	if revokedAt.Valid {
		token.RevokedAt = revokedAt.Time
	}
	return &token, nil
}

func (d *DB) TouchAPIToken(id int64, lastUsed time.Time) error {
	_, err := d.db.Exec("UPDATE api_tokens SET last_used_at = ? WHERE id = ?", utc(lastUsed), id)
	return err
}

func (d *DB) RevokeAPIToken(id int64, username string) error {
	_, err := d.db.Exec("UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND username = ?", utc(time.Now()), id, username)
	return err
}

// ListAPITokensByUser returns all non-revoked tokens for a given user.
// Token hashes are excluded from the result.
func (d *DB) ListAPITokensByUser(username string) ([]APIToken, error) {
	rows, err := d.db.Query(`
		SELECT id, username, name, token_prefix, scopes, created_at, last_used_at
		FROM api_tokens WHERE username = ? AND revoked_at IS NULL
		ORDER BY created_at DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []APIToken
	for rows.Next() {
		var token APIToken
		var scopesJSON string
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&token.ID, &token.Username, &token.Name, &token.TokenPrefix, &scopesJSON, &token.CreatedAt, &lastUsedAt); err != nil {
			return nil, err
		}
		if scopesJSON != "" {
			if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
				return nil, fmt.Errorf("unmarshal scopes for token %d: %w", token.ID, err)
			}
		}
		if lastUsedAt.Valid {
			token.LastUsedAt = lastUsedAt.Time
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// --- Auth User operations ---

// AuthUser represents a user stored in the database for authentication.
type AuthUser struct {
	Username     string
	PasswordHash string
}

// SaveAuthUser creates or updates an auth user. If the username already exists,
// the password hash and updated_at timestamp are overwritten.
func (d *DB) SaveAuthUser(username, passwordHash string) error {
	_, err := d.db.Exec(`
		INSERT INTO auth_users (username, password_hash)
		VALUES (?, ?)
		ON CONFLICT(username) DO UPDATE SET
			password_hash = excluded.password_hash,
			updated_at = CURRENT_TIMESTAMP`,
		username, passwordHash,
	)
	return err
}

// SeedAuthUser inserts a user only if the username does not already exist.
// This preserves any password changes made through the UI/API.
func (d *DB) SeedAuthUser(username, passwordHash string) error {
	_, err := d.db.Exec(`INSERT OR IGNORE INTO auth_users (username, password_hash) VALUES (?, ?)`,
		username, passwordHash,
	)
	return err
}

// ListAuthUsers returns all auth users ordered by username.
func (d *DB) ListAuthUsers() ([]AuthUser, error) {
	rows, err := d.db.Query("SELECT username, password_hash FROM auth_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []AuthUser
	for rows.Next() {
		var u AuthUser
		if err := rows.Scan(&u.Username, &u.PasswordHash); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
