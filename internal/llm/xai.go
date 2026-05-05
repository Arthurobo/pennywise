package llm

import (
	"context"
	"net/http"
)

// XAIBaseURL is the default REST root. xAI's Grok API is OpenAI-compatible at
// the wire-protocol level.
const XAIBaseURL = "https://api.x.ai/v1"

// XAIProvider is a thin alias around OpenAIProvider with a different base URL
// and provider name. xAI publishes an OpenAI-compatible /chat/completions
// endpoint; the request and response shapes are identical.
type XAIProvider struct {
	APIKey  string
	BaseURL string // empty → XAIBaseURL
	Client  *http.Client
}

func (p *XAIProvider) Name() string { return "xai" }

func (p *XAIProvider) inner() *OpenAIProvider {
	base := p.BaseURL
	if base == "" {
		base = XAIBaseURL
	}
	return &OpenAIProvider{APIKey: p.APIKey, BaseURL: base, Client: p.Client}
}

func (p *XAIProvider) Complete(ctx context.Context, r Request) (Response, error) {
	// xAI's chat completions are OpenAI-compatible at the wire level,
	// including the multimodal `image_url` parts shape. The narrower
	// JPG/PNG-only restriction is enforced upstream by callers via
	// ProviderImageMIMETypes("xai").
	return p.inner().Complete(ctx, r)
}

func (p *XAIProvider) Test(ctx context.Context, model string) error {
	if model == "" {
		model = DefaultModel("xai")
	}
	return p.inner().Test(ctx, model)
}
