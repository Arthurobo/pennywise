package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// CookieName is the session cookie name.
const CookieName = "pennywise_session"

// SessionLifetime is how long a new session remains valid.
const SessionLifetime = 30 * 24 * time.Hour

// SweepInterval is how often we delete expired sessions.
const SweepInterval = time.Hour

// Manager creates, validates, and revokes sessions backed by the SQLite store.
type Manager struct {
	q *sqlcgen.Queries
}

func NewManager(q *sqlcgen.Queries) *Manager {
	return &Manager{q: q}
}

// Create issues a new session, persists it, and writes a cookie on w.
// It returns the session ID.
func (m *Manager) Create(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	expires := now.Add(SessionLifetime)

	err = m.q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		ID:        id,
		CreatedAt: now.Unix(),
		ExpiresAt: expires.Unix(),
		UserAgent: nullString(truncate(r.UserAgent(), 255)),
		IpAddress: nullString(clientIP(r)),
	})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(SessionLifetime.Seconds()),
	})
	return id, nil
}

// Validate looks up the session referenced by the request cookie. Returns the
// session ID if valid; an empty string and nil error if no/invalid cookie.
func (m *Manager) Validate(ctx context.Context, r *http.Request) (string, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", nil
	}
	if c.Value == "" || len(c.Value) > 128 {
		return "", nil
	}
	s, err := m.q.GetSession(ctx, sqlcgen.GetSessionParams{
		ID:        c.Value,
		ExpiresAt: time.Now().UTC().Unix(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lookup session: %w", err)
	}
	return s.ID, nil
}

// Revoke deletes the session and clears the cookie.
func (m *Manager) Revoke(ctx context.Context, w http.ResponseWriter, sessionID string) {
	if sessionID != "" {
		if err := m.q.DeleteSession(ctx, sessionID); err != nil {
			slog.Warn("delete session", "error", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// RevokeAll deletes every persisted session (useful on password change).
func (m *Manager) RevokeAll(ctx context.Context) error {
	return m.q.DeleteAllSessions(ctx)
}

// StartSweeper runs a background goroutine that deletes expired sessions until ctx is canceled.
func (m *Manager) StartSweeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(SweepInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := m.q.DeleteExpiredSessions(ctx, time.Now().UTC().Unix())
				if err != nil {
					slog.Warn("session sweeper", "error", err)
					continue
				}
				if n > 0 {
					slog.Debug("session sweeper deleted expired sessions", "count", n)
				}
			}
		}
	}()
}

// EqualConstantTime is a thin wrapper that hides the byte conversion at call sites.
func EqualConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return truncate(xff, 64)
	}
	return truncate(r.RemoteAddr, 64)
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
