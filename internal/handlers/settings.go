package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

// Settings GET /settings — overview / profile tab.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "settings", map[string]any{
		"ActiveTab":      "profile",
		"Currencies":     models.Currencies,
		"Timezones":      models.CommonTimezones,
		"LLMStatus":      h.computeLLMStatus(r.Context()),
		"TelegramStatus": h.computeTelegramStatus(r.Context()),
		"BuildInfo": map[string]string{
			"Version":   h.Version,
			"Commit":    h.Commit,
			"BuildDate": h.BuildDate,
			"DBPath":    h.Cfg.DBPath(),
		},
	})
}

// UpdateProfile POST /settings/profile — display name + email.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	owner := auth.OwnerFromContext(r.Context())
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	name := strings.TrimSpace(r.PostFormValue("display_name"))
	if name == "" {
		http.Redirect(w, r, "/settings?error=name+required", http.StatusSeeOther)
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		http.Redirect(w, r, "/settings?error=invalid+email", http.StatusSeeOther)
		return
	}
	if err := h.Q.UpdateOwnerProfile(r.Context(), sqlcgen.UpdateOwnerProfileParams{
		Email: email, DisplayName: name, UpdatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	_ = owner
	http.Redirect(w, r, "/settings?ok=profile", http.StatusSeeOther)
}

// UpdatePassword POST /settings/password — requires current password.
func (h *Handler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	owner, err := h.Q.GetOwner(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	confirm := r.PostFormValue("new_password_confirm")
	if !auth.Verify(owner.PasswordHash, current) {
		http.Redirect(w, r, "/settings?error=current+password+incorrect", http.StatusSeeOther)
		return
	}
	if next != confirm {
		http.Redirect(w, r, "/settings?error=new+passwords+do+not+match", http.StatusSeeOther)
		return
	}
	hash, err := auth.Hash(next)
	if err != nil {
		http.Redirect(w, r, "/settings?error=password+too+short", http.StatusSeeOther)
		return
	}
	if err := h.Q.UpdateOwnerPassword(r.Context(), sqlcgen.UpdateOwnerPasswordParams{
		PasswordHash: hash, UpdatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	// Revoke all sessions (including this one) and force re-login.
	if err := h.Sessions.RevokeAll(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	h.Sessions.Revoke(r.Context(), w, auth.SessionIDFromContext(r.Context()))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// UpdatePreferences POST /settings/preferences — currency + timezone.
func (h *Handler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	code := strings.TrimSpace(r.PostFormValue("currency_code"))
	symbol := strings.TrimSpace(r.PostFormValue("currency_symbol"))
	tz := strings.TrimSpace(r.PostFormValue("timezone"))

	if c, ok := models.LookupCurrency(code); ok && symbol == "" {
		symbol = c.Symbol
	}
	if symbol == "" {
		http.Redirect(w, r, "/settings?error=currency+symbol+required", http.StatusSeeOther)
		return
	}
	if _, err := time.LoadLocation(tz); err != nil {
		http.Redirect(w, r, "/settings?error=invalid+timezone", http.StatusSeeOther)
		return
	}
	if err := h.Q.UpdateOwnerPreferences(r.Context(), sqlcgen.UpdateOwnerPreferencesParams{
		CurrencyCode: code, CurrencySymbol: symbol, Timezone: tz,
		UpdatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/settings?ok=preferences", http.StatusSeeOther)
}

// UpdateDashboardURL POST /settings/dashboard-url — public address users hit
// to reach the web UI. Surfaced back to the user via the Telegram bot.
func (h *Handler) UpdateDashboardURL(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	raw := strings.TrimSpace(r.PostFormValue("dashboard_url"))
	normalized, err := normalizeDashboardURL(raw)
	if err != nil {
		http.Redirect(w, r, "/settings?error="+err.Error(), http.StatusSeeOther)
		return
	}
	if err := h.Q.UpdateOwnerDashboardURL(r.Context(), sqlcgen.UpdateOwnerDashboardURLParams{
		DashboardUrl: normalized,
		UpdatedAt:    time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/settings?ok=dashboard-url", http.StatusSeeOther)
}

// normalizeDashboardURL validates a user-supplied dashboard URL and returns
// the canonical form (no trailing slash, no fragment/query, lower-case scheme
// and host). Returns a URL-safe error suitable for redirect query strings —
// keep messages short and free of special characters.
func normalizeDashboardURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("dashboard+url+required")
	}
	if len(raw) > 256 {
		return "", errors.New("dashboard+url+too+long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("invalid+dashboard+url")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("dashboard+url+must+be+http+or+https")
	}
	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// ExportDB GET /settings/export-db — streams a full database + secret key
// backup as a downloadable .zip file. Uses SQLite's VACUUM INTO to create a
// consistent snapshot even while the server is handling other requests.
func (h *Handler) ExportDB(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	filename := "pennywise-backup-" + time.Now().In(loc).Format("2006-01-02") + ".zip"

	tmp, err := os.CreateTemp("", "pennywise-backup-*.zip")
	if err != nil {
		serverError(w, err)
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := pwdb.Archive(h.Cfg.DBPath(), h.Cfg.SecretPath(), tmpPath); err != nil {
		serverError(w, err)
		return
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		serverError(w, err)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	http.ServeContent(w, r, filename, info.ModTime(), f)
}
