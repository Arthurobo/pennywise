package handlers

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/models"
)

// ExportCSV GET /export/csv — streams every matching expense as CSV.
//
// Filters are the same as /expenses, so users can bookmark a filter URL and
// swap "/expenses" → "/export/csv" to download that exact view.
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	filter, _, _ := h.parseExpenseFilter(r, loc)
	filter.Limit = 1_000_000 // effectively unlimited
	filter.Offset = 0

	rows, _, err := ListFilteredExpenses(r.Context(), h.DB, filter)
	if err != nil {
		serverError(w, err)
		return
	}

	filename := "pennywise-expenses-" + time.Now().In(loc).Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"id", "spent_at", "amount", "currency", "spent_on", "notes",
		"category", "ledger", "created_at",
	})
	for _, e := range rows {
		spentAt := time.Unix(e.SpentAt, 0).In(loc).Format(time.RFC3339)
		notes := ""
		if e.Notes.Valid {
			notes = e.Notes.String
		}
		category := ""
		if e.CategoryName.Valid {
			category = e.CategoryName.String
		}
		ledger := ""
		if e.LedgerName.Valid {
			ledger = e.LedgerName.String
		}
		_ = cw.Write([]string{
			strconv.FormatInt(e.ID, 10),
			spentAt,
			models.FormatAmount(e.Amount),
			owner.CurrencyCode,
			e.Description,
			notes,
			category,
			ledger,
			"",
		})
	}
}

// ExportLedgerCSV GET /ledgers/{id}/export/csv — exports all expenses for a
// specific ledger as CSV.
func (h *Handler) ExportLedgerCSV(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	l, err := h.Q.GetLedger(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			notFound(w)
			return
		}
		serverError(w, err)
		return
	}

	filter := ExpenseFilter{LedgerID: id, Limit: 1_000_000, Offset: 0}
	rows, _, err := ListFilteredExpenses(r.Context(), h.DB, filter)
	if err != nil {
		serverError(w, err)
		return
	}

	safeName := strings.ReplaceAll(strings.ReplaceAll(l.Name, "/", "-"), " ", "-")
	filename := "pennywise-" + safeName + "-" + time.Now().In(loc).Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"id", "spent_at", "amount", "currency", "spent_on", "notes",
		"category", "created_at",
	})
	for _, e := range rows {
		spentAt := time.Unix(e.SpentAt, 0).In(loc).Format(time.RFC3339)
		notes := ""
		if e.Notes.Valid {
			notes = e.Notes.String
		}
		category := ""
		if e.CategoryName.Valid {
			category = e.CategoryName.String
		}
		_ = cw.Write([]string{
			strconv.FormatInt(e.ID, 10),
			spentAt,
			models.FormatAmount(e.Amount),
			owner.CurrencyCode,
			e.Description,
			notes,
			category,
			"",
		})
	}
}
