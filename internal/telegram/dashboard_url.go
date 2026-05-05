package telegram

import (
	"net/url"
	"strings"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// dashboardURLHint formats the owner's configured dashboard URL for inclusion
// in user-facing bot messages. Returns "" if the URL is empty so callers can
// concatenate without producing a stray label.
func dashboardURLHint(owner sqlcgen.Owner) string {
	u := strings.TrimSpace(owner.DashboardUrl)
	if u == "" {
		return ""
	}
	return "🔗 *Dashboard:* " + EscapeMarkdown(u)
}

// dashboardURLButton returns a single-row inline keyboard pointing at the
// dashboard, or nil when the URL won't reach the user's phone (localhost,
// loopback). Telegram accepts http://localhost in URL buttons, but tapping
// it from a phone goes nowhere — better to omit and rely on the text hint
// in /start, /help, and pairing-success messages.
func dashboardURLButton(owner sqlcgen.Owner) []InlineKeyboardButton {
	raw := strings.TrimSpace(owner.DashboardUrl)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "localhost",
		host == "127.0.0.1",
		strings.HasPrefix(host, "127."),
		host == "0.0.0.0",
		host == "::1":
		return nil
	}
	return []InlineKeyboardButton{{Text: "🔗 Open dashboard", URL: raw}}
}
