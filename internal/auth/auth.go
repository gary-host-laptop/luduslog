// Package auth implements password hashing, sessions, CSRF tokens, email
// tokens and rate limiting.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"luduslog/internal/store"
)

type Service struct {
	Store *store.Store
}

type Session struct {
	Token     string
	TokenHash string
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
}

const sessionTTL = 30 * 24 * time.Hour

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// HashPassword returns an argon2id PHC string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const memory = 64 * 1024
	const iterations = 1
	const parallelism = 4
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an argon2id PHC hash in constant
// time with respect to the hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CreateSession stores a new session and returns its raw token.
func (a *Service) CreateSession(ctx context.Context, userID int64, ua, ip string) (*Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	exp := time.Now().UTC().Add(sessionTTL)
	hash := hashToken(token)
	_, err = a.Store.DB.ExecContext(ctx,
		`INSERT INTO sessions(token_hash,user_id,csrf,created_at,expires_at,user_agent,ip) VALUES(?,?,?,?,?,?,?)`,
		hash, userID, csrf, store.Now(), exp.Format(time.RFC3339), ua, ip)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, TokenHash: hash, UserID: userID, CSRF: csrf, ExpiresAt: exp}, nil
}

// LookupSession resolves a raw cookie token.
func (a *Service) LookupSession(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, store.ErrNotFound
	}
	hash := hashToken(token)
	var s Session
	var exp string
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT token_hash,user_id,csrf,expires_at FROM sessions WHERE token_hash=?`, hash).
		Scan(&s.TokenHash, &s.UserID, &s.CSRF, &exp)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	s.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
	if time.Now().UTC().After(s.ExpiresAt) {
		a.Store.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
		return nil, store.ErrNotFound
	}
	s.Token = token
	return &s, nil
}

func (a *Service) DestroySession(ctx context.Context, token string) error {
	_, err := a.Store.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hashToken(token))
	return err
}

func (a *Service) DestroyUserSessions(ctx context.Context, userID int64, exceptToken string) error {
	if exceptToken == "" {
		_, err := a.Store.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
		return err
	}
	_, err := a.Store.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, hashToken(exceptToken))
	return err
}

// CreateEmailToken returns a single-use token for verify/reset/deactivate.
func (a *Service) CreateEmailToken(ctx context.Context, userID int64, kind, email string, ttl time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	_, err = a.Store.DB.ExecContext(ctx,
		`INSERT INTO email_tokens(token_hash,user_id,kind,email,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		hashToken(token), userID, kind, email, store.Now(), time.Now().UTC().Add(ttl).Format(time.RFC3339))
	return token, err
}

// ConsumeEmailToken validates and deletes a token, returning its user and data.
func (a *Service) ConsumeEmailToken(ctx context.Context, token, kind string) (int64, string, error) {
	var userID int64
	var email, exp string
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT user_id,email,expires_at FROM email_tokens WHERE token_hash=? AND kind=?`,
		hashToken(token), kind).Scan(&userID, &email, &exp)
	if err != nil {
		return 0, "", store.ErrNotFound
	}
	expT, err := time.Parse(time.RFC3339, exp)
	if err != nil || time.Now().UTC().After(expT) {
		a.Store.DB.ExecContext(ctx, `DELETE FROM email_tokens WHERE token_hash=?`, hashToken(token))
		return 0, "", store.ErrNotFound
	}
	a.Store.DB.ExecContext(ctx, `DELETE FROM email_tokens WHERE token_hash=?`, hashToken(token))
	return userID, email, nil
}

// RateLimit is a fixed-window counter backed by SQLite. It returns true when
// the action is allowed.
func (a *Service) RateLimit(ctx context.Context, key string, limit int, window time.Duration) bool {
	now := time.Now().UTC()
	cutoff := now.Add(-window).Format(time.RFC3339)
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return true
	}
	defer tx.Rollback()
	var start string
	var count int
	err = tx.QueryRowContext(ctx, `SELECT window_start,count FROM rate_limits WHERE key=?`, key).Scan(&start, &count)
	if err != nil || start < cutoff {
		_, err = tx.ExecContext(ctx, `INSERT INTO rate_limits(key,window_start,count) VALUES(?,?,1)
			ON CONFLICT(key) DO UPDATE SET window_start=excluded.window_start,count=1`, key, now.Format(time.RFC3339))
		if err != nil {
			return true
		}
		tx.Commit()
		return true
	}
	if count >= limit {
		return false
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rate_limits SET count=count+1 WHERE key=?`, key); err != nil {
		return true
	}
	tx.Commit()
	return true
}

// SlowDown warns when an action is being repeated unusually quickly.
func (a *Service) SlowDown(ctx context.Context, key string, limit int, window time.Duration) bool {
	return !a.RateLimit(ctx, "slow:"+key, limit, window)
}
