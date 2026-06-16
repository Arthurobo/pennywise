package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

const expensesPerPage = 50

// Expenses GET /expenses — list with filters & pagination.
func (h *Handler) Expenses(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}

	filter, formData, filterErr := h.parseExpenseFilter(r, loc)
	filter.Limit = expensesPerPage
	filter.Offset = (page - 1) * expensesPerPage

	rows, total, err := ListFilteredExpenses(r.Context(), h.DB, filter)
	if err != nil {
		serverError(w, err)
		return
	}
	sum, err := SumFilteredExpenses(r.Context(), h.DB, filter)
	if err != nil {
		serverError(w, err)
		return
	}

	cats, err := h.Q.ListActiveCategories(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	leds, err := h.Q.ListActiveLedgers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	totalPages := int((total + expensesPerPage - 1) / expensesPerPage)
	if totalPages < 1 {
		totalPages = 1
	}

	filtersActive := 0
	if formData.From != "" || formData.To != "" || formData.LedgerID != "" ||
		formData.CategoryID != "" || formData.MinAmount != "" || formData.MaxAmount != "" {
		if formData.From != "" {
			filtersActive++
		}
		if formData.To != "" {
			filtersActive++
		}
		if formData.LedgerID != "" {
			filtersActive++
		}
		if formData.CategoryID != "" {
			filtersActive++
		}
		if formData.MinAmount != "" {
			filtersActive++
		}
		if formData.MaxAmount != "" {
			filtersActive++
		}
	}

	activeLedgerName := ""
	if formData.LedgerID != "" && formData.LedgerID != "none" {
		for _, l := range leds {
			if strconv.FormatInt(l.ID, 10) == formData.LedgerID {
				activeLedgerName = l.Name
				break
			}
		}
	}
	activeCategoryName := ""
	if formData.CategoryID != "" && formData.CategoryID != "none" {
		for _, c := range cats {
			if strconv.FormatInt(c.ID, 10) == formData.CategoryID {
				activeCategoryName = c.Name
				break
			}
		}
	}

	pageNums := pageNumbers(page, totalPages)

	data := map[string]any{
		"Rows":               rows,
		"Total":              total,
		"Sum":                sum,
		"Categories":         cats,
		"Ledgers":            leds,
		"Form":               formData,
		"FilterError":        filterErr,
		"FiltersActive":      filtersActive,
		"ActiveLedgerName":   activeLedgerName,
		"ActiveCategoryName": activeCategoryName,
		"Page":               page,
		"TotalPages":         totalPages,
		"Pages":              pageNums,
		"HasPrev":            page > 1,
		"HasNext":            page < totalPages,
		"PrevPage":           page - 1,
		"NextPage":           page + 1,
		"QueryString":        filterQueryString(formData),
	}

	if r.Header.Get("HX-Request") == "true" {
		h.renderPartial(w, r, "expenses", "expenses_list", data)
		return
	}
	h.renderPage(w, r, "expenses", data)
}

// NewExpenseForm GET /expenses/new
func (h *Handler) NewExpenseForm(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)
	cats, err := h.Q.ListActiveCategories(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	leds, err := h.Q.ListActiveLedgers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	h.renderPage(w, r, "expense_form", map[string]any{
		"Mode":                "new",
		"FormAction":          "/expenses",
		"Categories":          cats,
		"Ledgers":             leds,
		"DefaultDate":         time.Now().In(loc).Format("2006-01-02"),
		"PreselectedLedger":   r.URL.Query().Get("ledger"),
		"PreselectedCategory": r.URL.Query().Get("category"),
	})
}

// CreateExpense POST /expenses
func (h *Handler) CreateExpense(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	params, formErr := h.parseExpenseForm(r, loc, 0)
	if formErr != nil {
		h.renderExpenseFormError(w, r, "new", "/expenses", 0, formErr.Error(), r.PostForm)
		return
	}
	now := time.Now().UTC().Unix()
	params.CreatedAt = now
	params.UpdatedAt = now
	if _, err := h.Q.CreateExpense(r.Context(), params); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/expenses", http.StatusSeeOther)
}

// EditExpenseForm GET /expenses/{id}/edit
func (h *Handler) EditExpenseForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)
	exp, err := h.Q.GetExpense(r.Context(), id)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			notFound(w)
			return
		}
		serverError(w, err)
		return
	}
	cats, err := h.Q.ListActiveCategories(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	leds, err := h.Q.ListActiveLedgers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	h.renderPage(w, r, "expense_form", map[string]any{
		"Mode":       "edit",
		"FormAction": "/expenses/" + strconv.FormatInt(exp.ID, 10),
		"Method":     http.MethodPost,
		"Expense":    exp,
		"Categories": cats,
		"Ledgers":    leds,
		"AmountStr":  models.FormatAmount(exp.Amount),
		"DateStr":    time.Unix(exp.SpentAt, 0).In(loc).Format("2006-01-02"),
	})
}

// UpdateExpense POST /expenses/{id} (with optional _method=DELETE)
func (h *Handler) UpdateExpense(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}

	if strings.EqualFold(r.PostFormValue("_method"), http.MethodDelete) {
		h.deleteExpense(w, r, id)
		return
	}

	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)
	params, formErr := h.parseExpenseForm(r, loc, id)
	if formErr != nil {
		h.renderExpenseFormError(w, r, "edit", "/expenses/"+strconv.FormatInt(id, 10), id, formErr.Error(), r.PostForm)
		return
	}
	if err := h.Q.UpdateExpense(r.Context(), sqlcgen.UpdateExpenseParams{
		Amount: params.Amount, Description: params.Description, Notes: params.Notes,
		SpentAt: params.SpentAt, CategoryID: params.CategoryID, LedgerID: params.LedgerID,
		UpdatedAt: time.Now().UTC().Unix(), ID: id,
	}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/expenses", http.StatusSeeOther)
}

// BulkDeleteExpenses POST /expenses/bulk-delete — soft-deletes every selected
// expense in one transaction. The form sends `ids` as a repeated form field;
// invalid IDs are silently dropped (not an error worth surfacing).
func (h *Handler) BulkDeleteExpenses(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	raw := r.PostForm["ids"]
	ids := make([]int64, 0, len(raw))
	for _, s := range raw {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			ids = append(ids, v)
		}
	}
	if len(ids) == 0 {
		http.Redirect(w, r, "/expenses", http.StatusSeeOther)
		return
	}
	if _, err := BulkSoftDeleteExpenses(r.Context(), h.DB, ids); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/expenses", http.StatusSeeOther)
}

// DeleteExpense DELETE /expenses/{id} (HTMX hx-delete) — soft delete.
// The expense is moved to the trash and can be restored from the
// "Recently deleted" page. Permanent removal happens there or via the
// background sweeper after `owner.trash_retention_days`.
func (h *Handler) DeleteExpense(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		notFound(w)
		return
	}
	h.deleteExpense(w, r, id)
}

func (h *Handler) deleteExpense(w http.ResponseWriter, r *http.Request, id int64) {
	now := time.Now().UTC().Unix()
	if err := h.Q.SoftDeleteExpense(r.Context(), sqlcgen.SoftDeleteExpenseParams{
		DeletedAt: sql.NullInt64{Int64: now, Valid: true},
		UpdatedAt: now,
		ID:        id,
	}); err != nil {
		serverError(w, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		// Tell HTMX to refetch the listing.
		w.Header().Set("HX-Redirect", "/expenses")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/expenses", http.StatusSeeOther)
}

// --- helpers ---------------------------------------------------------------

func (h *Handler) parseExpenseForm(r *http.Request, loc *time.Location, _ int64) (sqlcgen.CreateExpenseParams, error) {
	amountStr := strings.TrimSpace(r.PostFormValue("amount"))
	desc := strings.TrimSpace(r.PostFormValue("description"))
	notes := strings.TrimSpace(r.PostFormValue("notes"))
	dateStr := strings.TrimSpace(r.PostFormValue("spent_at"))
	categoryRaw := r.PostFormValue("category_id")
	ledgerRaw := r.PostFormValue("ledger_id")

	if desc == "" {
		return sqlcgen.CreateExpenseParams{}, errors.New("\"Spent on\" is required")
	}
	cents, err := models.ParseAmount(amountStr)
	if err != nil {
		return sqlcgen.CreateExpenseParams{}, err
	}
	if cents == 0 {
		return sqlcgen.CreateExpenseParams{}, errors.New("amount must be greater than zero")
	}
	day, ok := parseYMD(dateStr, loc)
	if !ok {
		return sqlcgen.CreateExpenseParams{}, errors.New("date must be YYYY-MM-DD")
	}

	catID, catOK := nullableSelectID(categoryRaw)
	ledID, ledOK := nullableSelectID(ledgerRaw)

	return sqlcgen.CreateExpenseParams{
		Amount:      cents,
		Description: desc,
		Notes:       optString(notes),
		SpentAt:     day.Unix(),
		CategoryID:  optInt64(catID, catOK),
		LedgerID:    optInt64(ledID, ledOK),
	}, nil
}

func (h *Handler) renderExpenseFormError(w http.ResponseWriter, r *http.Request, mode, action string, id int64, msg string, form map[string][]string) {
	cats, _ := h.Q.ListActiveCategories(r.Context())
	leds, _ := h.Q.ListActiveLedgers(r.Context())
	h.renderPage(w, r, "expense_form", map[string]any{
		"Mode":       mode,
		"FormAction": action,
		"ID":         id,
		"Categories": cats,
		"Ledgers":    leds,
		"Error":      msg,
		"AmountStr":  formAt(form, "amount"),
		"DateStr":    formAt(form, "spent_at"),
		"DescStr":    formAt(form, "description"),
		"NotesStr":   formAt(form, "notes"),
		"CategoryID": formAt(form, "category_id"),
		"LedgerID":   formAt(form, "ledger_id"),
	})
}

func formAt(form map[string][]string, key string) string {
	if v, ok := form[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// nullableSelectID returns NULL for "", the literal-NULL sentinel "none", or a positive int wrapped.
// We accept "none" so the form can distinguish "no ledger" from "no filter applied".
func nullableSelectID(raw string) (idOrZero int64, present bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "none" || raw == "0" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// parseExpenseFilter pulls filter values out of the query string.
func (h *Handler) parseExpenseFilter(r *http.Request, loc *time.Location) (ExpenseFilter, expenseFilterForm, string) {
	q := r.URL.Query()
	form := expenseFilterForm{
		From:       q.Get("from"),
		To:         q.Get("to"),
		LedgerID:   q.Get("ledger_id"),
		CategoryID: q.Get("category_id"),
		Search:     strings.TrimSpace(q.Get("q")),
		MinAmount:  q.Get("min"),
		MaxAmount:  q.Get("max"),
	}
	f := ExpenseFilter{Search: form.Search}

	if form.From != "" {
		t, ok := parseYMD(form.From, loc)
		if !ok {
			return f, form, "From date must be YYYY-MM-DD"
		}
		f.From = t.Unix()
	}
	if form.To != "" {
		t, ok := parseYMD(form.To, loc)
		if !ok {
			return f, form, "To date must be YYYY-MM-DD"
		}
		// inclusive end-of-day
		f.To = t.Add(24 * time.Hour).Unix()
	}
	switch form.LedgerID {
	case "", "any":
		// no constraint
	case "none":
		f.LedgerID = -1
	default:
		n, _ := strconv.ParseInt(form.LedgerID, 10, 64)
		if n > 0 {
			f.LedgerID = n
		}
	}
	switch form.CategoryID {
	case "", "any":
	case "none":
		f.CategoryID = -1
	default:
		n, _ := strconv.ParseInt(form.CategoryID, 10, 64)
		if n > 0 {
			f.CategoryID = n
		}
	}
	if form.MinAmount != "" {
		c, err := models.ParseAmount(form.MinAmount)
		if err != nil {
			return f, form, "Min amount: " + err.Error()
		}
		f.MinAmount = c
	}
	if form.MaxAmount != "" {
		c, err := models.ParseAmount(form.MaxAmount)
		if err != nil {
			return f, form, "Max amount: " + err.Error()
		}
		f.MaxAmount = c
	}
	return f, form, ""
}

type expenseFilterForm struct {
	From, To, LedgerID, CategoryID, Search, MinAmount, MaxAmount string
}

// filterQueryString rebuilds the canonical query string for pagination links.
func filterQueryString(f expenseFilterForm) string {
	pairs := []string{}
	add := func(k, v string) {
		if v == "" {
			return
		}
		pairs = append(pairs, k+"="+urlEscape(v))
	}
	add("from", f.From)
	add("to", f.To)
	add("ledger_id", f.LedgerID)
	add("category_id", f.CategoryID)
	add("q", f.Search)
	add("min", f.MinAmount)
	add("max", f.MaxAmount)
	return strings.Join(pairs, "&")
}

func urlEscape(s string) string {
	return url.QueryEscape(s)
}

func pageNumbers(current, total int) []int {
	if total <= 7 {
		pages := make([]int, total)
		for i := range pages {
			pages[i] = i + 1
		}
		return pages
	}
	pages := []int{1}
	if current > 4 {
		pages = append(pages, -1) // ellipsis sentinel
	}
	start := current - 2
	if start < 2 {
		start = 2
	}
	end := current + 2
	if end > total-1 {
		end = total - 1
	}
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	if current < total-3 {
		pages = append(pages, -1) // ellipsis sentinel
	}
	pages = append(pages, total)
	return pages
}
