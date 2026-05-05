package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

// Reports GET /reports?from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *Handler) Reports(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	loc := models.LoadLocation(owner.Timezone)

	to := time.Now().In(loc)
	from := to.AddDate(0, -1, 0)

	if v := r.URL.Query().Get("from"); v != "" {
		if t, ok := parseYMD(v, loc); ok {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, ok := parseYMD(v, loc); ok {
			to = t.Add(24 * time.Hour) // inclusive end-of-day
		}
	}
	if from.After(to) {
		from, to = to, from
	}

	ctx := r.Context()
	startOfDayFrom := startOfDay(from, loc)

	total, err := h.Q.TotalSpentBetween(ctx, sqlcgen.TotalSpentBetweenParams{
		SpentAt: startOfDayFrom.Unix(), SpentAt_2: to.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	cats, err := h.Q.SpendingByCategoryBetween(ctx, sqlcgen.SpendingByCategoryBetweenParams{
		SpentAt: startOfDayFrom.Unix(), SpentAt_2: to.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	leds, err := h.Q.SpendingByLedgerBetween(ctx, sqlcgen.SpendingByLedgerBetweenParams{
		SpentAt: startOfDayFrom.Unix(), SpentAt_2: to.Unix(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	top, err := h.Q.TopExpensesBetween(ctx, sqlcgen.TopExpensesBetweenParams{
		SpentAt: startOfDayFrom.Unix(), SpentAt_2: to.Unix(), Limit: 10,
	})
	if err != nil {
		serverError(w, err)
		return
	}

	// Time series: pick aggregation granularity from range length.
	rangeDays := int(to.Sub(startOfDayFrom).Hours() / 24)
	timeSeries, err := buildTimeSeries(ctx, h.Q, startOfDayFrom, to, rangeDays, loc)
	if err != nil {
		serverError(w, err)
		return
	}

	h.renderPage(w, r, "reports", map[string]any{
		"From":           startOfDayFrom.Format("2006-01-02"),
		"To":             to.Add(-time.Second).Format("2006-01-02"),
		"Total":          total,
		"Categories":     cats,
		"Ledgers":        leds,
		"TopExpenses":    top,
		"CategoryJSON":   categoriesPie(cats),
		"LedgerJSON":     ledgersPie(leds),
		"TimeSeriesJSON": timeSeries,
		"TimeSeriesGran": granularityLabel(rangeDays),
	})
}

func categoriesPie(rows []sqlcgen.SpendingByCategoryBetweenRow) string {
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

func ledgersPie(rows []sqlcgen.SpendingByLedgerBetweenRow) string {
	type slice struct {
		Label string `json:"label"`
		Value int64  `json:"value"`
	}
	out := make([]slice, 0, len(rows))
	for _, r := range rows {
		out = append(out, slice{Label: r.LedgerName, Value: r.Total})
	}
	return jsonMust(out)
}

// buildTimeSeries pulls daily totals and rebuckets them into weekly/monthly
// rollups depending on the range length. Daily series are filled out so missing
// days appear as zero, keeping the chart's x-axis uniform.
func buildTimeSeries(ctx context.Context, q *sqlcgen.Queries, from, to time.Time, rangeDays int, loc *time.Location) (string, error) {
	daily, err := q.DailySpendingBetween(ctx, sqlcgen.DailySpendingBetweenParams{
		SpentAt: from.Unix(), SpentAt_2: to.Unix(),
	})
	if err != nil {
		return "", err
	}
	type point struct {
		Label string `json:"label"`
		Value int64  `json:"value"`
	}

	switch {
	case rangeDays <= 90:
		out := make([]point, 0, rangeDays+1)
		byDay := make(map[string]int64, len(daily))
		for _, r := range daily {
			byDay[time.Unix(r.Day, 0).In(loc).Format("2006-01-02")] = r.Total
		}
		for i := 0; i <= rangeDays; i++ {
			d := from.AddDate(0, 0, i)
			if !d.Before(to) {
				break
			}
			out = append(out, point{Label: d.Format("Jan 2"), Value: byDay[d.Format("2006-01-02")]})
		}
		return jsonMust(out), nil

	case rangeDays <= 365:
		// Weekly buckets keyed by ISO week starting Monday.
		buckets := map[string]int64{}
		labels := map[string]string{}
		for _, r := range daily {
			d := time.Unix(r.Day, 0).In(loc)
			y, w := d.ISOWeek()
			key := isoWeekKey(y, w)
			buckets[key] += r.Total
			if _, ok := labels[key]; !ok {
				labels[key] = "W" + itoa(w) + " " + itoa(y)
			}
		}
		// Walk weeks in order
		out := []point{}
		cursor := startOfISOWeek(from, loc)
		for cursor.Before(to) {
			y, w := cursor.ISOWeek()
			key := isoWeekKey(y, w)
			out = append(out, point{Label: "W" + itoa(w), Value: buckets[key]})
			cursor = cursor.AddDate(0, 0, 7)
		}
		return jsonMust(out), nil

	default:
		// Monthly buckets keyed YYYY-MM.
		buckets := map[string]int64{}
		for _, r := range daily {
			d := time.Unix(r.Day, 0).In(loc)
			key := d.Format("2006-01")
			buckets[key] += r.Total
		}
		out := []point{}
		cursor := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, loc)
		for cursor.Before(to) {
			key := cursor.Format("2006-01")
			out = append(out, point{Label: cursor.Format("Jan 06"), Value: buckets[key]})
			cursor = cursor.AddDate(0, 1, 0)
		}
		return jsonMust(out), nil
	}
}

func isoWeekKey(y, w int) string { return itoa(y) + "-" + itoa(w) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// startOfISOWeek returns the Monday of the ISO week containing t, in loc.
func startOfISOWeek(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-(wd-1), 0, 0, 0, 0, loc)
}

func granularityLabel(rangeDays int) string {
	switch {
	case rangeDays <= 90:
		return "Daily"
	case rangeDays <= 365:
		return "Weekly"
	default:
		return "Monthly"
	}
}
