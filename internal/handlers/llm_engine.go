package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/llm"
)

// errLLMNotConfigured is returned by LLMEngine when no row exists in
// llm_config yet (first-run, before the user has set a provider). The
// caller is expected to surface a "configure your LLM in Settings" hint.
var errLLMNotConfigured = errors.New("llm not configured")

// LLMEngine builds a ready-to-use *llm.Engine from the persisted config.
//
// This is the web-handler equivalent of the Telegram dispatcher's
// d.loadLLM — it loads the config, decrypts the API key via h.Secrets,
// and wraps the provider in an Engine with the standard DB logger and
// the configured timeout.
//
// Returns errLLMNotConfigured when no config row exists; other errors
// are surfaced verbatim. Callers should typically map errLLMNotConfigured
// to a friendly "set this up in Settings → LLM" message.
func (h *Handler) LLMEngine(ctx context.Context) (sqlcgen.LlmConfig, *llm.Engine, error) {
	cfg, err := h.Q.GetLLMConfig(ctx)
	if err != nil {
		if errors.Is(err, errSQLNoRows) {
			return sqlcgen.LlmConfig{}, nil, errLLMNotConfigured
		}
		return sqlcgen.LlmConfig{}, nil, fmt.Errorf("load llm config: %w", err)
	}
	if h.LLMProvider == nil && (!cfg.ApiKeyEncrypted.Valid || cfg.ApiKeyEncrypted.String == "") {
		return cfg, nil, errLLMNotConfigured
	}
	var provider llm.Provider
	if h.LLMProvider != nil {
		// Test-only seam: skip Factory + decrypt and use the injected
		// provider. Mirrors DispatcherOpts.LLMProvider on the bot side.
		provider = h.LLMProvider
	} else {
		apiKey, err := h.Secrets.Open(cfg.ApiKeyEncrypted.String)
		if err != nil {
			return cfg, nil, fmt.Errorf("decrypt api key: %w", err)
		}
		provider, err = llm.Factory(cfg.Provider, apiKey, nil)
		if err != nil {
			return cfg, nil, err
		}
	}
	timeout := h.Cfg.LLMTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return cfg, &llm.Engine{
		Provider: provider,
		Logger:   &llm.DBLogger{Q: h.Q},
		Timeout:  timeout,
	}, nil
}
