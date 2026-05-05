package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// isHexColor accepts strings of the form "#rrggbb" with case-insensitive
// hex digits. Manual check rather than regex — faster, allocation-free, and
// keeps the codebase regex-free.
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// SettingsCategories GET /settings/categories — list + add form combined.
func (h *Handler) SettingsCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.Q.ListAllCategories(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	h.renderPage(w, r, "settings_categories", map[string]any{
		"Categories":     cats,
		"ActiveTab":      "categories",
		"LLMStatus":      h.computeLLMStatus(r.Context()),
		"TelegramStatus": h.computeTelegramStatus(r.Context()),
	})
}

// CreateCategory POST /settings/categories
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	color := strings.TrimSpace(r.PostFormValue("color"))
	if name == "" {
		http.Redirect(w, r, "/settings/categories?error=name+required", http.StatusSeeOther)
		return
	}
	if !isHexColor(color) {
		color = "#6b7280"
	}
	if _, err := h.Q.CreateCategory(r.Context(), sqlcgen.CreateCategoryParams{
		Name: name, Color: color, CreatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		if isUniqueConstraint(err) {
			http.Redirect(w, r, "/settings/categories?error=name+exists", http.StatusSeeOther)
			return
		}
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/categories", http.StatusSeeOther)
}

// UpdateCategory POST /settings/categories/{id}
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	cur, err := h.Q.GetCategory(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			notFound(w)
			return
		}
		serverError(w, err)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = cur.Name
	}
	color := strings.TrimSpace(r.PostFormValue("color"))
	if !isHexColor(color) {
		color = cur.Color
	}
	if err := h.Q.UpdateCategory(r.Context(), sqlcgen.UpdateCategoryParams{
		Name: name, Color: color, Icon: cur.Icon, ID: id,
	}); err != nil {
		if isUniqueConstraint(err) {
			http.Redirect(w, r, "/settings/categories?error=name+exists", http.StatusSeeOther)
			return
		}
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/categories", http.StatusSeeOther)
}

// ArchiveCategory POST /settings/categories/{id}/archive — toggles archived.
// Categories with expenses cannot be deleted (per spec); archive is the only path.
func (h *Handler) ArchiveCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	cur, err := h.Q.GetCategory(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			notFound(w)
			return
		}
		serverError(w, err)
		return
	}
	newVal := int64(1)
	if cur.IsArchived == 1 {
		newVal = 0
	}
	if err := h.Q.SetCategoryArchived(r.Context(), sqlcgen.SetCategoryArchivedParams{
		IsArchived: newVal, ID: id,
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/categories", http.StatusSeeOther)
}
