package handlers

import (
	"encoding/json"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

type chartPayload struct {
	Labels []string `json:"labels"`
	Values []int64  `json:"values"`
}

// buildDailyChartPayload turns DB rows into a labels+values series, filling
// missing days with zero so the chart x-axis is uniform.
func buildDailyChartPayload(rows []sqlcgen.DailySpendingBetweenRow, from time.Time, days int, loc *time.Location) chartPayload {
	byDay := make(map[string]int64, len(rows))
	for _, r := range rows {
		key := time.Unix(r.Day, 0).In(loc).Format("2006-01-02")
		byDay[key] = r.Total
	}
	out := chartPayload{
		Labels: make([]string, days),
		Values: make([]int64, days),
	}
	for i := 0; i < days; i++ {
		d := from.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		out.Labels[i] = d.Format("Jan 2")
		out.Values[i] = byDay[key]
	}
	return out
}

// buildDailyChart returns the JSON-encoded chart payload for embedding in HTML.
func buildDailyChart(rows []sqlcgen.DailySpendingBetweenRow, from time.Time, days int, loc *time.Location) string {
	b, _ := json.Marshal(buildDailyChartPayload(rows, from, days, loc))
	return string(b)
}
