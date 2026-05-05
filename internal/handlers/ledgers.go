package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

// Ledgers GET /ledgers — grid of cards. ?show=archived to view archived.
func (h *Handler) Ledgers(w http.ResponseWriter, r *http.Request) {
	showArchived := r.URL.Query().Get("show") == "archived"

	var rows []sqlcgen.Ledger
	var err error
	if showArchived {
		rows, err = h.Q.ListArchivedLedgers(r.Context())
	} else {
		rows, err = h.Q.ListActiveLedgers(r.Context())
	}
	if err != nil {
		serverError(w, err)
		return
	}

	summaries := make([]LedgerSummary, 0, len(rows))
	for _, l := range rows {
		total, err := h.Q.LedgerTotalSpent(r.Context(), nullInt64(l.ID))
		if err != nil {
			serverError(w, err)
			return
		}
		s := LedgerSummary{Ledger: l, Total: total}
		if l.BudgetAmount.Valid && l.BudgetAmount.Int64 > 0 {
			s.HasBudget = true
			s.Budget = l.BudgetAmount.Int64
			s.PercentUsed = (total * 100) / l.BudgetAmount.Int64
			s.StatusClass = budgetStatus(s.PercentUsed)
		}
		summaries = append(summaries, s)
	}

	h.renderPage(w, r, "ledgers", map[string]any{
		"Ledgers":      summaries,
		"ShowArchived": showArchived,
	})
}

// NewLedgerForm GET /ledgers/new
func (h *Handler) NewLedgerForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "ledger_form", map[string]any{
		"Mode":       "new",
		"FormAction": "/ledgers",
	})
}

// CreateLedger POST /ledgers
func (h *Handler) CreateLedger(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	params, formErr := parseLedgerForm(r, loc)
	if formErr != "" {
		h.renderLedgerFormError(w, r, "new", "/ledgers", 0, formErr, r.PostForm)
		return
	}
	now := time.Now().UTC().Unix()
	params.CreatedAt = now
	params.UpdatedAt = now
	if _, err := h.Q.CreateLedger(r.Context(), params); err != nil {
		if isUniqueConstraint(err) {
			h.renderLedgerFormError(w, r, "new", "/ledgers", 0, "A ledger with that name already exists.", r.PostForm)
			return
		}
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/ledgers", http.StatusSeeOther)
}

// LedgerDetail GET /ledgers/{id}
func (h *Handler) LedgerDetail(w http.ResponseWriter, r *http.Request) {
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

	expRows, err := h.Q.ListExpensesByLedger(r.Context(), nullInt64(id))
	if err != nil {
		serverError(w, err)
		return
	}
	total, err := h.Q.LedgerTotalSpent(r.Context(), nullInt64(id))
	if err != nil {
		serverError(w, err)
		return
	}
	catRows, err := h.Q.SpendingByCategoryForLedger(r.Context(), nullInt64(id))
	if err != nil {
		serverError(w, err)
		return
	}
	dailyRows, err := h.Q.DailySpendingForLedger(r.Context(), nullInt64(id))
	if err != nil {
		serverError(w, err)
		return
	}

	summary := LedgerSummary{Ledger: l, Total: total}
	if l.BudgetAmount.Valid && l.BudgetAmount.Int64 > 0 {
		summary.HasBudget = true
		summary.Budget = l.BudgetAmount.Int64
		summary.PercentUsed = (total * 100) / l.BudgetAmount.Int64
		summary.StatusClass = budgetStatus(summary.PercentUsed)
	}

	h.renderPage(w, r, "ledger_detail", map[string]any{
		"L":            summary,
		"Expenses":     expRows,
		"CategoryJSON": ledgerCategoryChartJSON(catRows),
		"DailyJSON":    ledgerDailyChartJSON(dailyRows, loc),
		"ExpenseCount": len(expRows),
	})
}

// EditLedgerForm GET /ledgers/{id}/edit
func (h *Handler) EditLedgerForm(w http.ResponseWriter, r *http.Request) {
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
	h.renderPage(w, r, "ledger_form", map[string]any{
		"Mode":       "edit",
		"FormAction": "/ledgers/" + strconv.FormatInt(l.ID, 10),
		"Ledger":     l,
		"BudgetStr":  optionalAmountStr(l.BudgetAmount),
		"StartStr":   optionalDateStr(l.StartDate, loc),
		"EndStr":     optionalDateStr(l.EndDate, loc),
	})
}

// UpdateLedger POST /ledgers/{id}
func (h *Handler) UpdateLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	params, formErr := parseLedgerForm(r, loc)
	if formErr != "" {
		h.renderLedgerFormError(w, r, "edit", "/ledgers/"+strconv.FormatInt(id, 10), id, formErr, r.PostForm)
		return
	}

	if err := h.Q.UpdateLedger(r.Context(), sqlcgen.UpdateLedgerParams{
		Name: params.Name, Description: params.Description,
		BudgetAmount: params.BudgetAmount, StartDate: params.StartDate, EndDate: params.EndDate,
		UpdatedAt: time.Now().UTC().Unix(), ID: id,
	}); err != nil {
		if isUniqueConstraint(err) {
			h.renderLedgerFormError(w, r, "edit", "/ledgers/"+strconv.FormatInt(id, 10), id, "A ledger with that name already exists.", r.PostForm)
			return
		}
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/ledgers/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// ArchiveLedger POST /ledgers/{id}/archive — toggles archived flag.
func (h *Handler) ArchiveLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	l, err := h.Q.GetLedger(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			notFound(w)
			return
		}
		serverError(w, err)
		return
	}
	newVal := int64(1)
	if l.IsArchived == 1 {
		newVal = 0
	}
	if err := h.Q.SetLedgerArchived(r.Context(), sqlcgen.SetLedgerArchivedParams{
		IsArchived: newVal, UpdatedAt: time.Now().UTC().Unix(), ID: id,
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/ledgers", http.StatusSeeOther)
}

// DeleteLedger DELETE /ledgers/{id}.
//
// Refuses if the ledger has expenses unless ?force=1, in which case the
// expenses' ledger_id is set to NULL first (the spec's "warn and ask the user
// to confirm" path).
func (h *Handler) DeleteLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	force := r.URL.Query().Get("force") == "1" || r.PostFormValue("force") == "1"

	count, err := h.Q.LedgerExpenseCount(r.Context(), nullInt64(id))
	if err != nil {
		serverError(w, err)
		return
	}
	if count > 0 && !force {
		http.Error(w, "ledger has expenses; resubmit with force=1 to detach them", http.StatusConflict)
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	q := h.Q.WithTx(tx)
	if count > 0 {
		if err := q.ClearLedgerOnExpenses(r.Context(), sqlcgen.ClearLedgerOnExpensesParams{
			UpdatedAt: time.Now().UTC().Unix(), LedgerID: nullInt64(id),
		}); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := q.DeleteLedger(r.Context(), id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		serverError(w, err)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/ledgers")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/ledgers", http.StatusSeeOther)
}

// --- form parsing -----------------------------------------------------------

func parseLedgerForm(r *http.Request, loc *time.Location) (sqlcgen.CreateLedgerParams, string) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	desc := strings.TrimSpace(r.PostFormValue("description"))
	budget := strings.TrimSpace(r.PostFormValue("budget_amount"))
	startStr := strings.TrimSpace(r.PostFormValue("start_date"))
	endStr := strings.TrimSpace(r.PostFormValue("end_date"))

	if name == "" {
		return sqlcgen.CreateLedgerParams{}, "Name is required."
	}

	var p sqlcgen.CreateLedgerParams
	p.Name = name
	p.Description = optString(desc)

	if budget != "" {
		cents, err := models.ParseAmount(budget)
		if err != nil {
			return p, "Budget: " + err.Error()
		}
		p.BudgetAmount = optInt64(cents, true)
	}
	if startStr != "" {
		t, ok := parseYMD(startStr, loc)
		if !ok {
			return p, "Start date must be YYYY-MM-DD."
		}
		p.StartDate = optInt64(t.Unix(), true)
	}
	if endStr != "" {
		t, ok := parseYMD(endStr, loc)
		if !ok {
			return p, "End date must be YYYY-MM-DD."
		}
		p.EndDate = optInt64(t.Unix(), true)
	}
	if p.StartDate.Valid && p.EndDate.Valid && p.EndDate.Int64 < p.StartDate.Int64 {
		return p, "End date must be on or after start date."
	}
	return p, ""
}

func (h *Handler) renderLedgerFormError(w http.ResponseWriter, r *http.Request, mode, action string, id int64, msg string, form map[string][]string) {
	h.renderPage(w, r, "ledger_form", map[string]any{
		"Mode":       mode,
		"FormAction": action,
		"ID":         id,
		"Error":      msg,
		"NameStr":    formAt(form, "name"),
		"DescStr":    formAt(form, "description"),
		"BudgetStr":  formAt(form, "budget_amount"),
		"StartStr":   formAt(form, "start_date"),
		"EndStr":     formAt(form, "end_date"),
	})
}

func optionalAmountStr(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	return models.FormatAmount(n.Int64)
}

func optionalDateStr(n sql.NullInt64, loc *time.Location) string {
	if !n.Valid {
		return ""
	}
	return time.Unix(n.Int64, 0).In(loc).Format("2006-01-02")
}

// isUniqueConstraint detects modernc.org/sqlite UNIQUE constraint failures.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: UNIQUE")
}

// --- chart payloads ---------------------------------------------------------

func ledgerCategoryChartJSON(rows []sqlcgen.SpendingByCategoryForLedgerRow) string {
	type slice struct {
		Label string `json:"label"`
		Value int64  `json:"value"`
		Color string `json:"color"`
	}
	out := make([]slice, 0, len(rows))
	for _, r := range rows {
		out = append(out, slice{Label: r.CategoryName, Value: r.Total, Color: r.CategoryColor})
	}
	return jsonMust(out)
}

func ledgerDailyChartJSON(rows []sqlcgen.DailySpendingForLedgerRow, loc *time.Location) string {
	type point struct {
		Label string `json:"label"`
		Value int64  `json:"value"`
	}
	out := make([]point, 0, len(rows))
	for _, r := range rows {
		out = append(out, point{
			Label: time.Unix(r.Day, 0).In(loc).Format("Jan 2"),
			Value: r.Total,
		})
	}
	return jsonMust(out)
}
