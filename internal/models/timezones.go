package models

import "time"

// CommonTimezones is shown in dropdowns. The list is intentionally small;
// users can also paste an arbitrary IANA name into a free-form input.
var CommonTimezones = []string{
	"UTC",
	"Africa/Lagos",
	"Africa/Johannesburg",
	"Africa/Nairobi",
	"Africa/Accra",
	"Africa/Cairo",
	"Europe/London",
	"Europe/Paris",
	"Europe/Berlin",
	"Europe/Amsterdam",
	"Europe/Madrid",
	"Europe/Stockholm",
	"Europe/Istanbul",
	"America/New_York",
	"America/Chicago",
	"America/Denver",
	"America/Los_Angeles",
	"America/Toronto",
	"America/Mexico_City",
	"America/Sao_Paulo",
	"America/Argentina/Buenos_Aires",
	"Asia/Dubai",
	"Asia/Kolkata",
	"Asia/Singapore",
	"Asia/Hong_Kong",
	"Asia/Shanghai",
	"Asia/Tokyo",
	"Asia/Seoul",
	"Australia/Sydney",
	"Australia/Melbourne",
	"Pacific/Auckland",
}

// LoadLocation is a thin wrapper that returns UTC if the zone can't be loaded.
func LoadLocation(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}
