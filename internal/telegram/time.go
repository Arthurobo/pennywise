package telegram

import "time"

// startOfDay returns midnight of t in loc.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// startOfWeek returns Monday 00:00 of the ISO week containing t in loc.
// Per spec: weeks are Monday–Sunday in the user's timezone.
func startOfWeek(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	wd := int(t.Weekday())
	if wd == 0 { // Sunday in Go is 0
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-(wd-1), 0, 0, 0, 0, loc)
}

// startOfMonth returns the first of the calendar month of t in loc.
func startOfMonth(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
}

// startOfYear returns Jan 1 00:00 of the calendar year of t in loc.
func startOfYear(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc)
}
