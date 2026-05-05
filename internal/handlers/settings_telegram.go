package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/telegram"
)

// SettingsTelegram GET /settings/telegram
//
// The page is gated behind a verified LLM provider per the v2 spec.
// If gated, the form is hidden and the user is redirected to the LLM tab.
func (h *Handler) SettingsTelegram(w http.ResponseWriter, r *http.Request) {
	llmStatus := h.computeLLMStatus(r.Context())
	if llmStatus.State != "ok" {
		h.renderPage(w, r, "settings_telegram", map[string]any{
			"ActiveTab":      "telegram",
			"Locked":         true,
			"LLMStatus":      llmStatus,
			"TelegramStatus": h.computeTelegramStatus(r.Context()),
		})
		return
	}

	cfg, hasCfg := h.loadTelegramConfig(r.Context())

	data := map[string]any{
		"ActiveTab":      "telegram",
		"Locked":         false,
		"LLMStatus":      llmStatus,
		"TelegramStatus": h.computeTelegramStatus(r.Context()),
	}
	switch {
	case !hasCfg:
		data["Step"] = "create"
	case !cfg.ChatID.Valid:
		data["Step"] = "pair"
		data["BotUsername"] = cfg.BotUsername
		data["PairingCode"] = cfg.PairingCode.String
		data["PairingExpires"] = pairingExpiresLabel(cfg.PairingExpiresAt)
	default:
		data["Step"] = "connected"
		data["BotUsername"] = cfg.BotUsername
		data["ChatID"] = cfg.ChatID.Int64
		data["Enabled"] = cfg.Enabled == 1
	}
	h.renderPage(w, r, "settings_telegram", data)
}

// SaveTelegramBot POST /settings/telegram/bot — validates the token via
// getMe and persists the encrypted token + bot username.
func (h *Handler) SaveTelegramBot(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	token := strings.TrimSpace(r.PostFormValue("bot_token"))
	if token == "" {
		http.Redirect(w, r, "/settings/telegram?error=token+required", http.StatusSeeOther)
		return
	}

	// Validate before persisting — this catches bad tokens with a clear
	// error message instead of a silent "bot won't poll" later.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	me, err := telegram.ValidateToken(ctx, token, nil)
	if err != nil {
		http.Redirect(w, r, "/settings/telegram?error="+urlEscape("Telegram rejected the token: "+err.Error()), http.StatusSeeOther)
		return
	}

	sealed, err := h.Secrets.Seal(token)
	if err != nil {
		serverError(w, err)
		return
	}
	now := time.Now().UTC().Unix()
	if err := h.Q.UpsertTelegramBot(r.Context(), sqlcgen.UpsertTelegramBotParams{
		BotTokenEncrypted: sealed,
		BotUsername:       me.Username,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/telegram", http.StatusSeeOther)
}

// GenerateTelegramPairing POST /settings/telegram/pair — creates a fresh
// pairing code with a 10-minute TTL.
func (h *Handler) GenerateTelegramPairing(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.loadTelegramConfig(r.Context()); !ok {
		http.Redirect(w, r, "/settings/telegram?error=save+token+first", http.StatusSeeOther)
		return
	}
	code, err := telegram.GeneratePairingCode()
	if err != nil {
		serverError(w, err)
		return
	}
	now := time.Now().UTC()
	if err := h.Q.SetTelegramPairingCode(r.Context(), sqlcgen.SetTelegramPairingCodeParams{
		PairingCode:      sql.NullString{String: code, Valid: true},
		PairingExpiresAt: sql.NullInt64{Int64: now.Add(time.Duration(telegram.PairingTTL) * time.Second).Unix(), Valid: true},
		UpdatedAt:        now.Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/telegram", http.StatusSeeOther)
}

// PollPairingStatus GET /settings/telegram/status — HTMX polling target.
//
// Renders one of three small fragments:
//   - "still waiting"
//   - "pairing complete, refreshing"
//   - "code expired, generate a new one"
func (h *Handler) PollPairingStatus(w http.ResponseWriter, r *http.Request) {
	cfg, ok := h.loadTelegramConfig(r.Context())
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if cfg.ChatID.Valid {
		// Tell HTMX to fully refresh into the connected view.
		w.Header().Set("HX-Redirect", "/settings/telegram")
		w.WriteHeader(http.StatusOK)
		return
	}
	state := "waiting"
	if cfg.PairingExpiresAt.Valid && cfg.PairingExpiresAt.Int64 < time.Now().Unix() {
		state = "expired"
	}
	h.renderPartial(w, r, "settings_telegram", "telegram_pair_status", map[string]any{
		"State":          state,
		"BotUsername":    cfg.BotUsername,
		"PairingCode":    cfg.PairingCode.String,
		"PairingExpires": pairingExpiresLabel(cfg.PairingExpiresAt),
	})
}

// SetTelegramEnabled POST /settings/telegram/enable, /disable
func (h *Handler) SetTelegramEnabled(w http.ResponseWriter, r *http.Request) {
	enabled := int64(1)
	if strings.HasSuffix(r.URL.Path, "/disable") {
		enabled = 0
	}
	if err := h.Q.SetTelegramEnabled(r.Context(), sqlcgen.SetTelegramEnabledParams{
		Enabled: enabled, UpdatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/telegram", http.StatusSeeOther)
}

// DisconnectTelegramChat POST /settings/telegram/disconnect — clears chat_id
// only, keeping the bot token in place so the user can re-pair.
func (h *Handler) DisconnectTelegramChat(w http.ResponseWriter, r *http.Request) {
	if err := h.Q.ClearTelegramChatID(r.Context(), time.Now().UTC().Unix()); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/telegram", http.StatusSeeOther)
}

// RemoveTelegramBot POST /settings/telegram/remove — wipes the entire row.
func (h *Handler) RemoveTelegramBot(w http.ResponseWriter, r *http.Request) {
	if err := h.Q.DeleteTelegramConfig(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/telegram", http.StatusSeeOther)
}

func (h *Handler) loadTelegramConfig(ctx context.Context) (sqlcgen.TelegramConfig, bool) {
	cfg, err := h.Q.GetTelegramConfig(ctx)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			return sqlcgen.TelegramConfig{}, false
		}
		return sqlcgen.TelegramConfig{}, false
	}
	return cfg, true
}

func pairingExpiresLabel(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	t := time.Unix(n.Int64, 0).UTC()
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	if mins > 0 {
		return time.Duration(mins).String() + "m " + time.Duration(secs).String()
	}
	return t.Format("15:04 MST")
}
