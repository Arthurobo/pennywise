package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Arthurobo/pennywise/internal/auth"
)

// LoginForm GET /login
func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	if auth.OwnerFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	next := r.URL.Query().Get("next")
	h.renderPage(w, r, "login", map[string]any{"Next": next})
}

// Login POST /login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	password := r.PostFormValue("password")
	next := r.PostFormValue("next")

	owner, err := h.Q.GetOwner(r.Context())
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			// No owner exists yet — first-run setup hasn't been completed.
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		serverError(w, err)
		return
	}

	if !strings.EqualFold(owner.Email, email) || !auth.Verify(owner.PasswordHash, password) {
		h.renderPage(w, r, "login", map[string]any{
			"Error": "Email or password is incorrect.",
			"Email": email,
			"Next":  next,
		})
		return
	}

	if _, err := h.Sessions.Create(r.Context(), w, r); err != nil {
		serverError(w, err)
		return
	}

	dest := safeNext(next)
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// Logout POST /logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sid := auth.SessionIDFromContext(r.Context())
	h.Sessions.Revoke(r.Context(), w, sid)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeNext rejects open-redirects and falls back to /dashboard.
func safeNext(s string) string {
	if s == "" {
		return "/dashboard"
	}
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return s
	}
	return "/dashboard"
}
