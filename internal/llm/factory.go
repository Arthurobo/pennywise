package llm

import (
	"errors"
	"net/http"
	"time"
)

// Factory constructs a Provider for a given configuration.
//
// providerKey is the value stored in llm_config.provider ("openai",
// "anthropic", "gemini", "xai"). Each provider uses its hardcoded vendor
// base URL; the per-instance BaseURL field on the provider structs exists
// only so tests can point at httptest.Server.
func Factory(providerKey, apiKey string, client *http.Client) (Provider, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	switch providerKey {
	case "openai":
		return &OpenAIProvider{APIKey: apiKey, Client: client}, nil
	case "anthropic":
		return &AnthropicProvider{APIKey: apiKey, Client: client}, nil
	case "gemini":
		return &GeminiProvider{APIKey: apiKey, Client: client}, nil
	case "xai":
		return &XAIProvider{APIKey: apiKey, Client: client}, nil
	default:
		return nil, errors.New("llm: unknown provider " + providerKey)
	}
}
