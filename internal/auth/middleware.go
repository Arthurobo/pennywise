package auth

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

type ctxKey int

const (
	ctxOwner ctxKey = iota
	ctxSessionID
	ctxCSRFToken
)

// OwnerFromContext returns the owner attached by RequireAuth, or nil.
func OwnerFromContext(ctx context.Context) *sqlcgen.Owner {
	v, _ := ctx.Value(ctxOwner).(*sqlcgen.Owner)
	return v
}

// SessionIDFromContext returns the active session ID attached by AttachSession, or "".
func SessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxSessionID).(string)
	return v
}

// CSRFTokenFromContext returns the per-request CSRF token, or "".
func CSRFTokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxCSRFToken).(string)
	return v
}

// AttachCSRF ensures a CSRF id cookie exists and stashes the token in the request context.
func AttachCSRF(c *CSRF) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := c.EnsureID(w, r)
			if err != nil {
				slog.Error("csrf id", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), ctxCSRFToken, c.Token(id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// VerifyCSRF rejects mutating requests whose CSRF token does not match.
func VerifyCSRF(c *CSRF) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if !c.Validate(r) {
				http.Error(w, "CSRF token invalid or missing", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AttachSession looks up the current session (if any) and the owner, and attaches them.
// It does NOT enforce auth — that's RequireAuth's job. This runs on every request so
// templates can render headers based on login state.
func AttachSession(m *Manager, q *sqlcgen.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sid, err := m.Validate(r.Context(), r)
			if err != nil {
				slog.Warn("validate session", "error", err)
			}
			ctx := r.Context()
			if sid != "" {
				ctx = context.WithValue(ctx, ctxSessionID, sid)
				if owner, err := q.GetOwner(ctx); err == nil {
					ctx = context.WithValue(ctx, ctxOwner, &owner)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth redirects unauthenticated users to /login (preserving the destination).
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if OwnerFromContext(r.Context()) == nil {
			next := url.QueryEscape(r.URL.RequestURI())
			http.Redirect(w, r, "/login?next="+next, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSetup redirects to /setup if first-run setup has not been completed.
// initFn is called per request; the handlers package wires it to the cached app_state lookup.
func RequireSetup(initFn func(context.Context) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !initFn(r.Context()) {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RejectIfInitialized returns 404 once setup has completed, hiding /setup.
func RejectIfInitialized(initFn func(context.Context) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if initFn(r.Context()) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
