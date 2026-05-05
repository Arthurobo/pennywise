package handlers

import "context"

// SettingsStatus is the dot+label for the LLM/Telegram nav entries.
type SettingsStatus struct {
	State string // "ok", "warn", "off"
	Label string // tooltip / aria-label
}

func (h *Handler) computeLLMStatus(ctx context.Context) SettingsStatus {
	cfg, err := h.Q.GetLLMConfig(ctx)
	if err != nil {
		return SettingsStatus{State: "off", Label: "Not configured"}
	}
	if cfg.Enabled != 1 || cfg.LastTestSuccess != 1 {
		return SettingsStatus{State: "warn", Label: "Configured but not verified"}
	}
	return SettingsStatus{State: "ok", Label: "Configured"}
}

func (h *Handler) computeTelegramStatus(ctx context.Context) SettingsStatus {
	cfg, err := h.Q.GetTelegramConfig(ctx)
	if err != nil {
		return SettingsStatus{State: "off", Label: "Not configured"}
	}
	if !cfg.ChatID.Valid {
		return SettingsStatus{State: "warn", Label: "Bot saved, awaiting pairing"}
	}
	if cfg.Enabled != 1 {
		return SettingsStatus{State: "warn", Label: "Paired but disabled"}
	}
	return SettingsStatus{State: "ok", Label: "Connected"}
}
