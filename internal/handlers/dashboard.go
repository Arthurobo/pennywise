package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

// LedgerSummary is the dashboard card view for an active ledger.
type LedgerSummary struct {
	Ledger      sqlcgen.Ledger
	Total       int64
	HasBudget   bool
	Budget      int64
	PercentUsed int64
	StatusClass string // tailwind color class for the bar
}

// Dashboard GET /dashboard
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)
	now := time.Now().In(loc)

	startToday := startOfDay(now, loc)
	startMonth := startOfMonth(now, loc)
	startYear := startOfYear(now, loc)
	endNow := now.Add(time.Second)

	ctx := r.Context()

	today, err := h.Q.TotalSpentBetween(ctx, sqlcgen.TotalSpentBetweenParams{
		SpentAt: startToday.Unix(), SpentAt_2: endNow.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	month, err := h.Q.TotalSpentBetween(ctx, sqlcgen.TotalSpentBetweenParams{
		SpentAt: startMonth.Unix(), SpentAt_2: endNow.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	year, err := h.Q.TotalSpentBetween(ctx, sqlcgen.TotalSpentBetweenParams{
		SpentAt: startYear.Unix(), SpentAt_2: endNow.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}

	recent, err := h.Q.ListRecentExpenses(ctx, 5)
	if err != nil {
		serverError(w, err)
		return
	}

	ledgers, err := h.Q.ListActiveLedgers(ctx)
	if err != nil {
		serverError(w, err)
		return
	}

	summaries := make([]LedgerSummary, 0, len(ledgers))
	for _, l := range ledgers {
		ledgerID := nullInt64(l.ID)
		total, err := h.Q.LedgerTotalSpent(ctx, ledgerID)
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

	// Daily spending for the last 30 days (chart payload as JSON).
	chartFrom := startOfDay(now.AddDate(0, 0, -29), loc)
	rows, err := h.Q.DailySpendingBetween(ctx, sqlcgen.DailySpendingBetweenParams{
		SpentAt: chartFrom.Unix(), SpentAt_2: endNow.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	chartJSON := buildDailyChart(rows, chartFrom, 30, loc)

	h.renderPage(w, r, "dashboard", map[string]any{
		"TodayTotal": today,
		"MonthTotal": month,
		"YearTotal":  year,
		"Recent":     recent,
		"Ledgers":    summaries,
		"ChartJSON":  chartJSON,
		"MonthLabel": now.Format("January 2006"),
		"YearLabel":  now.Format("2006"),
	})
}

// DailySpendingJSON GET /api/charts/daily-spending?days=30
func (h *Handler) DailySpendingJSON(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)
	now := time.Now().In(loc)

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if d, err := parseDays(v); err == nil {
			days = d
		}
	}
	from := startOfDay(now.AddDate(0, 0, -(days-1)), loc)

	rows, err := h.Q.DailySpendingBetween(r.Context(), sqlcgen.DailySpendingBetweenParams{
		SpentAt: from.Unix(), SpentAt_2: now.Add(time.Second).Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildDailyChartPayload(rows, from, days, loc))
}

func budgetStatus(pct int64) string {
	switch {
	case pct >= 100:
		return "bg-red-600"
	case pct >= 80:
		return "bg-amber-500"
	default:
		return "bg-green-500"
	}
}

func parseDays(s string) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errBadDays
		}
		n = n*10 + int(r-'0')
		if n > 366 {
			return 366, nil
		}
	}
	if n < 1 {
		return 0, errBadDays
	}
	return n, nil
}

var errBadDays = jsonErr("invalid days")

type jsonErrType string

func (e jsonErrType) Error() string { return string(e) }
func jsonErr(s string) jsonErrType  { return jsonErrType(s) }
