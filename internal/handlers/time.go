package handlers

import "time"

func formatDate(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format("Jan 2, 2006")
}

func formatDateTime(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format("Jan 2, 2006 3:04 PM")
}

func formatYMD(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format("2006-01-02")
}

// startOfDay returns midnight of the given moment in loc.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// startOfMonth returns the first of the month in loc.
func startOfMonth(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
}

// startOfYear returns Jan 1 in loc.
func startOfYear(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc)
}

// parseYMD parses a YYYY-MM-DD string in loc, returning the start of that day in UTC unix.
func parseYMD(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
