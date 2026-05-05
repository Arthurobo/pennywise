package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

const trashPerPage = 50

// Trash GET /expenses/trash — list deleted expenses with restore + permanent
// delete actions. The auto-purge cutoff is shown so the user knows when each
// row will vanish.
func (h *Handler) Trash(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}

	rows, err := h.Q.ListDeletedExpenses(r.Context(), sqlcgen.ListDeletedExpensesParams{
		Limit:  trashPerPage,
		Offset: int64((page - 1) * trashPerPage),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	total, err := h.Q.CountDeletedExpenses(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	totalPages := int((total + trashPerPage - 1) / trashPerPage)
	if totalPages < 1 {
		totalPages = 1
	}

	// Compute when each row will auto-purge (deleted_at + retention).
	retention := time.Duration(owner.TrashRetentionDays) * 24 * time.Hour
	type trashRow struct {
		Row       sqlcgen.ListDeletedExpensesRow
		PurgesAt  time.Time
		ExpiresIn string
	}
	enriched := make([]trashRow, 0, len(rows))
	for _, r := range rows {
		when := time.Unix(0, 0)
		if r.DeletedAt.Valid {
			when = time.Unix(r.DeletedAt.Int64, 0)
		}
		purges := when.Add(retention)
		enriched = append(enriched, trashRow{
			Row:       r,
			PurgesAt:  purges,
			ExpiresIn: humanizeUntil(time.Until(purges)),
		})
	}

	h.renderPage(w, r, "trash", map[string]any{
		"Rows":       enriched,
		"Total":      total,
		"Page":       page,
		"TotalPages": totalPages,
		"HasPrev":    page > 1,
		"HasNext":    page < totalPages,
		"PrevPage":   page - 1,
		"NextPage":   page + 1,
		"Retention":  owner.TrashRetentionDays,
		"Timezone":   loc.String(),
	})
}

// RestoreExpense POST /expenses/trash/{id}/restore — moves a row out of trash.
func (h *Handler) RestoreExpense(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	exp, err := h.Q.GetDeletedExpense(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			notFound(w)
			return
		}
		serverError(w, err)
		return
	}
	if !exp.DeletedAt.Valid {
		// Already restored — treat as idempotent success.
		http.Redirect(w, r, "/expenses/trash", http.StatusSeeOther)
		return
	}
	if err := h.Q.RestoreExpense(r.Context(), sqlcgen.RestoreExpenseParams{
		UpdatedAt: time.Now().UTC().Unix(),
		ID:        id,
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/expenses/trash?ok=restored", http.StatusSeeOther)
}

// HardDeleteExpense POST /expenses/trash/{id}/delete — permanent removal.
// Only operates on rows already in trash (the SQL guards this), so a misfire
// can't bypass the trash safety net.
func (h *Handler) HardDeleteExpense(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	if err := h.Q.HardDeleteExpense(r.Context(), id); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/expenses/trash?ok=purged", http.StatusSeeOther)
}

// EmptyTrash POST /expenses/trash/empty — permanent removal of every trashed row.
func (h *Handler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Q.EmptyTrash(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/expenses/trash?ok=emptied", http.StatusSeeOther)
}

// UpdateTrashRetention POST /settings/trash-retention — owner-controlled
// auto-purge window. Capped at [1, 365] days.
func (h *Handler) UpdateTrashRetention(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	v, err := strconv.Atoi(r.PostFormValue("trash_retention_days"))
	if err != nil || v < 1 || v > 365 {
		http.Redirect(w, r, "/settings?error=retention+must+be+1..365", http.StatusSeeOther)
		return
	}
	if err := h.Q.UpdateOwnerTrashRetention(r.Context(), sqlcgen.UpdateOwnerTrashRetentionParams{
		TrashRetentionDays: int64(v),
		UpdatedAt:          time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/settings?ok=trash-retention", http.StatusSeeOther)
}

// humanizeUntil renders a time.Duration as "in 12 days", "in 4 hours",
// "expires soon", or "now". Used by the trash UI to show when each row
// auto-purges.
func humanizeUntil(d time.Duration) string {
	if d <= 0 {
		return "any moment"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins < 1 {
			return "any moment"
		}
		if mins == 1 {
			return "in 1 minute"
		}
		return "in " + strconv.Itoa(mins) + " minutes"
	}
	if d < 24*time.Hour {
		hrs := int(d.Hours())
		if hrs == 1 {
			return "in 1 hour"
		}
		return "in " + strconv.Itoa(hrs) + " hours"
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "in 1 day"
	}
	return "in " + strconv.Itoa(days) + " days"
}

// suppress unused-import warning if sql isn't otherwise referenced
var _ = sql.NullInt64{}
