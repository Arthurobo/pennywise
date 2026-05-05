// Package handlers contains the HTTP handlers grouped by feature.
//
// Every handler hangs off the Handler struct so deps (queries, renderer, auth,
// config) are wired once at startup and shared. Handlers render HTML via the
// shared renderer; small utility helpers (render, badRequest, etc.) live in
// this file.
package handlers

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/config"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/models"
	"github.com/Arthurobo/pennywise/internal/templates"
)

const appStateInitializedKey = "initialized"

// AutomationSupervisor is the contract handlers use to nudge the Telegram
// supervisor when the LLM or Telegram config changes — so the bot can start
// or stop without waiting for the next 30-second tick.
type AutomationSupervisor interface {
	Trigger()
}

// Handler bundles every dependency the HTTP handlers need.
type Handler struct {
	Cfg        config.Config
	DB         *sql.DB
	Q          *sqlcgen.Queries
	Renderer   *templates.Renderer
	Sessions   *auth.Manager
	CSRF       *auth.CSRF
	Secrets    *auth.SecretBox
	Supervisor AutomationSupervisor
	Version    string
	Commit     string
	BuildDate  string

	// LLMProvider, when non-nil, is used by LLMEngine instead of building a
	// real provider via llm.Factory. Test-only seam — production callers
	// must leave this nil so the configured provider + decrypted API key
	// are used.
	LLMProvider llm.Provider

	initialized atomic.Bool
}

// New constructs a Handler. Call WarmInitFlag once after construction.
// Supervisor and Secrets may be nil during testing; the LLM/Telegram tabs
// will degrade gracefully.
func New(cfg config.Config, db *sql.DB, q *sqlcgen.Queries, r *templates.Renderer, sm *auth.Manager, c *auth.CSRF, secrets *auth.SecretBox, supervisor AutomationSupervisor, version, commit, buildDate string) *Handler {
	return &Handler{
		Cfg: cfg, DB: db, Q: q, Renderer: r, Sessions: sm, CSRF: c,
		Secrets: secrets, Supervisor: supervisor,
		Version: version, Commit: commit, BuildDate: buildDate,
	}
}

// triggerSupervisor pokes the Telegram supervisor if one is wired.
func (h *Handler) triggerSupervisor() {
	if h.Supervisor != nil {
		h.Supervisor.Trigger()
	}
}

// WarmInitFlag loads app_state.initialized into the cached flag at startup.
func (h *Handler) WarmInitFlag(ctx context.Context) error {
	v, err := h.Q.GetAppState(ctx, appStateInitializedKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.initialized.Store(false)
			return nil
		}
		return err
	}
	h.initialized.Store(strings.EqualFold(v, "true"))
	return nil
}

// IsInitialized returns the cached initialized flag.
func (h *Handler) IsInitialized(_ context.Context) bool { return h.initialized.Load() }

// renderPage executes a template with the standard chrome data.
func (h *Handler) renderPage(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	owner := auth.OwnerFromContext(r.Context())
	data["Owner"] = owner
	data["CSRFToken"] = auth.CSRFTokenFromContext(r.Context())
	data["ActivePath"] = activePath(r.URL.Path)
	data["DevMode"] = h.Cfg.IsDevelopment()
	data["Version"] = h.Version
	if owner != nil {
		data["CurrencySymbol"] = owner.CurrencySymbol
		data["CurrencyCode"] = owner.CurrencyCode
		data["Timezone"] = owner.Timezone
	} else {
		data["CurrencySymbol"] = "$"
		data["CurrencyCode"] = "USD"
		data["Timezone"] = "UTC"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Renderer.Render(w, page, data); err != nil {
		slog.Error("render", "page", page, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// renderPartial renders a single named template (no layout) for HTMX swaps.
func (h *Handler) renderPartial(w http.ResponseWriter, r *http.Request, page, partial string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	owner := auth.OwnerFromContext(r.Context())
	data["Owner"] = owner
	data["CSRFToken"] = auth.CSRFTokenFromContext(r.Context())
	if owner != nil {
		data["CurrencySymbol"] = owner.CurrencySymbol
	} else {
		data["CurrencySymbol"] = "$"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Renderer.RenderTemplate(w, page, partial, data); err != nil {
		slog.Error("render partial", "partial", partial, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// TemplateFuncs returns the funcs the renderer needs (money formatting bound to the owner).
func (h *Handler) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"money": func(cents int64, symbol string) string {
			if symbol == "" {
				symbol = "$"
			}
			return models.FormatMoney(cents, symbol)
		},
		"amount": func(cents int64) string { return models.FormatAmount(cents) },
		"formatDate": func(unix int64, tz string) string {
			loc := models.LoadLocation(tz)
			return formatDate(unix, loc)
		},
		"formatDateTime": func(unix int64, tz string) string {
			loc := models.LoadLocation(tz)
			return formatDateTime(unix, loc)
		},
		"formatYMD": func(unix int64, tz string) string {
			loc := models.LoadLocation(tz)
			return formatYMD(unix, loc)
		},
		"hasValue": func(s sql.NullString) bool { return s.Valid && s.String != "" },
		"nullStr": func(s sql.NullString) string {
			if s.Valid {
				return s.String
			}
			return ""
		},
		"nullInt": func(n sql.NullInt64) int64 {
			if n.Valid {
				return n.Int64
			}
			return 0
		},
		"nullIntPresent":    func(n sql.NullInt64) bool { return n.Valid },
		"nullStringPresent": func(s sql.NullString) bool { return s.Valid && s.String != "" },
		"int64Add":          func(a, b int64) int64 { return a + b },
		"int64Sub":          func(a, b int64) int64 { return a - b },
	}
}

// activePath maps a request path to a top-level nav identifier.
func activePath(p string) string {
	switch {
	case p == "/dashboard" || p == "/":
		return "dashboard"
	case strings.HasPrefix(p, "/expenses"):
		return "expenses"
	case strings.HasPrefix(p, "/ledgers"):
		return "ledgers"
	case strings.HasPrefix(p, "/reports"):
		return "reports"
	case strings.HasPrefix(p, "/settings"):
		return "settings"
	}
	return ""
}

// --- HTTP error helpers -----------------------------------------------------

func badRequest(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}

func notFound(w http.ResponseWriter) {
	http.Error(w, "not found", http.StatusNotFound)
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("handler", "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// parseID extracts a positive int64 path parameter.
func parseID(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// formField returns the trimmed value of a form field.
func formField(r *http.Request, name string) string {
	return strings.TrimSpace(r.PostFormValue(name))
}
