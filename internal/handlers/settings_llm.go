package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/llm"
)

// SettingsLLM GET /settings/llm
func (h *Handler) SettingsLLM(w http.ResponseWriter, r *http.Request) {
	cfg, hasCfg := h.loadLLMConfig(r.Context())
	selectedProvider := "openai"
	selectedModel := llm.DefaultModel("openai")
	hasKey := false

	if hasCfg {
		selectedProvider = cfg.Provider
		selectedModel = cfg.TextModel
		hasKey = cfg.ApiKeyEncrypted.Valid && cfg.ApiKeyEncrypted.String != ""
	}

	h.renderPage(w, r, "settings_llm", map[string]any{
		"ActiveTab":        "llm",
		"Providers":        llm.ProvidersInOrder,
		"SelectedProvider": selectedProvider,
		"SelectedModel":    selectedModel,
		"HasKey":           hasKey,
		"Models":           llm.Catalog[selectedProvider],
		"Enabled":          hasCfg && cfg.Enabled == 1,
		"LastTestAt":       nullTimeFmt(cfg.LastTestAt),
		"LastTestSuccess":  hasCfg && cfg.LastTestSuccess == 1,
		"LastTestError":    cfg.LastTestError.String,
		"LLMStatus":        h.computeLLMStatus(r.Context()),
		"TelegramStatus":   h.computeTelegramStatus(r.Context()),
	})
}

// LLMModelOptions GET /settings/llm/models?provider=openai
//
// HTMX target: refreshes the model dropdown when the provider select changes.
func (h *Handler) LLMModelOptions(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if _, ok := llm.LookupProvider(provider); !ok {
		badRequest(w, "unknown provider")
		return
	}
	h.renderPartial(w, r, "settings_llm", "llm_model_options", map[string]any{
		"Models":        llm.Catalog[provider],
		"SelectedModel": llm.DefaultModel(provider),
	})
}

// SaveLLMConfig POST /settings/llm
func (h *Handler) SaveLLMConfig(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}
	provider := r.PostFormValue("provider")
	model := r.PostFormValue("model")
	apiKey := r.PostFormValue("api_key")

	info, ok := llm.LookupProvider(provider)
	if !ok {
		http.Redirect(w, r, "/settings/llm?error=unknown+provider", http.StatusSeeOther)
		return
	}
	if !llm.IsValidModel(provider, model) {
		http.Redirect(w, r, "/settings/llm?error=unknown+model+for+"+info.Key, http.StatusSeeOther)
		return
	}

	// Encrypt the key if the user actually entered one. Empty input means
	// "keep the existing one" (matching the form's "Already set" affordance).
	var sealed sql.NullString
	if apiKey != "" {
		s, err := h.Secrets.Seal(apiKey)
		if err != nil {
			serverError(w, err)
			return
		}
		sealed = sql.NullString{String: s, Valid: true}
	}

	now := time.Now().UTC().Unix()
	if err := h.Q.UpsertLLMConfig(r.Context(), sqlcgen.UpsertLLMConfigParams{
		Provider:        provider,
		ApiKeyEncrypted: sealed,
		TextModel:       model,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/llm?ok=saved", http.StatusSeeOther)
}

// TestLLMConnection POST /settings/llm/test
//
// Returns an HTMX-swappable banner with the result. Sets enabled=1 on success.
func (h *Handler) TestLLMConnection(w http.ResponseWriter, r *http.Request) {
	cfg, ok := h.loadLLMConfig(r.Context())
	if !ok {
		h.renderTestResult(w, r, false, "Save the configuration first.")
		return
	}
	if !cfg.ApiKeyEncrypted.Valid || cfg.ApiKeyEncrypted.String == "" {
		h.renderTestResult(w, r, false, "No API key on file.")
		return
	}
	apiKey, err := h.Secrets.Open(cfg.ApiKeyEncrypted.String)
	if err != nil {
		h.renderTestResult(w, r, false, "Couldn't decrypt the stored API key.")
		return
	}
	provider, err := llm.Factory(cfg.Provider, apiKey, nil)
	if err != nil {
		h.renderTestResult(w, r, false, err.Error())
		return
	}

	timeout := h.Cfg.LLMTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	now := time.Now().UTC()
	testErr := provider.Test(ctx, cfg.TextModel)

	var msg sql.NullString
	success := int64(0)
	if testErr == nil {
		success = 1
	} else {
		msg = sql.NullString{String: testErr.Error(), Valid: true}
	}
	if dbErr := h.Q.UpdateLLMTestResult(r.Context(), sqlcgen.UpdateLLMTestResultParams{
		LastTestAt:      sql.NullInt64{Int64: now.Unix(), Valid: true},
		LastTestSuccess: success,
		LastTestError:   msg,
		UpdatedAt:       now.Unix(),
	}); dbErr != nil {
		serverError(w, dbErr)
		return
	}
	if testErr == nil {
		// Auto-enable on first successful test.
		if err := h.Q.SetLLMEnabled(r.Context(), sqlcgen.SetLLMEnabledParams{
			Enabled: 1, UpdatedAt: now.Unix(),
		}); err != nil {
			serverError(w, err)
			return
		}
		h.triggerSupervisor()
		h.renderTestResult(w, r, true, "Connection successful — LLM is enabled.")
		return
	}
	h.renderTestResult(w, r, false, testErr.Error())
}

// DisableLLM POST /settings/llm/disable
func (h *Handler) DisableLLM(w http.ResponseWriter, r *http.Request) {
	if err := h.Q.SetLLMEnabled(r.Context(), sqlcgen.SetLLMEnabledParams{
		Enabled: 0, UpdatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		serverError(w, err)
		return
	}
	h.triggerSupervisor()
	http.Redirect(w, r, "/settings/llm?ok=disabled", http.StatusSeeOther)
}

func (h *Handler) renderTestResult(w http.ResponseWriter, r *http.Request, ok bool, msg string) {
	h.renderPartial(w, r, "settings_llm", "llm_test_result", map[string]any{
		"OK":      ok,
		"Message": msg,
	})
}

func (h *Handler) loadLLMConfig(ctx context.Context) (sqlcgen.LlmConfig, bool) {
	cfg, err := h.Q.GetLLMConfig(ctx)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			return sqlcgen.LlmConfig{}, false
		}
		return sqlcgen.LlmConfig{}, false
	}
	return cfg, true
}

func nullTimeFmt(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	return time.Unix(n.Int64, 0).UTC().Format("Jan 2, 2006 15:04 MST")
}
