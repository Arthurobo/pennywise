// Package llm wraps the four supported text-completion providers behind a
// single Provider interface and routes every call through Engine, which
// timeouts, logs, and reports back to the rest of the codebase.
//
// Providers wrap each vendor's REST API directly with net/http — no SDK
// per provider. Adding a fifth provider is a single file ~150 lines.
package llm

import (
	"context"
	"errors"
)

// ErrVisionUnsupported is returned by providers that can't handle images on
// the requested model. The caller (Telegram dispatcher) maps this to a
// user-facing "switch to a vision-capable model" message.
var ErrVisionUnsupported = errors.New("llm: vision not supported by this provider/model")

// ImageInput is one inline image attached to a Request. Providers base64-encode
// the bytes per their wire format. Empty Data is invalid and rejected.
type ImageInput struct {
	MIMEType string // "image/jpeg", "image/png", "image/webp", "image/heic", "application/pdf"
	Data     []byte
}

// Purpose is the high-level reason for an LLM call. Recorded in llm_call_log.
type Purpose string

const (
	// PurposeParseExpense is the unified message-classification call. After
	// the regex purge, every Telegram free-text message goes through this
	// one purpose — it covers expense parsing AND query intent in one call.
	PurposeParseExpense Purpose = "parse_expense"
	PurposeTest         Purpose = "test"
)

// Provider is the contract every LLM vendor implementation satisfies.
//
// Complete sends a chat-style completion and returns the raw response text
// and (where available) token counts. The caller — typically Engine and the
// defensive JSON parser — owns parsing.
//
// Test sends a tiny probe ("reply with OK") and returns nil if the API key
// works. It MUST be cheap; users hit "Test connection" repeatedly.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Test(ctx context.Context, model string) error
}

// Request is the provider-agnostic shape of one completion call.
type Request struct {
	Model        string
	SystemPrompt string
	UserMessage  string

	// Images is the optional set of inline image attachments. When non-empty,
	// the provider sends them alongside UserMessage as multimodal content.
	// Only providers/models flagged Vision=true in the catalog accept images;
	// others return ErrVisionUnsupported.
	Images []ImageInput

	// JSONMode asks the provider to return strict JSON. Each provider
	// implements this differently (OpenAI: response_format, Anthropic:
	// assistant prefill with `{`, Gemini: responseMimeType, xAI: same as
	// OpenAI). The defensive parser still cleans whatever comes back.
	JSONMode bool

	// MaxTokens caps the response length. 0 means provider default.
	MaxTokens int

	// Temperature: 0 is deterministic-ish, 1 is creative. Default 0 for
	// expense parsing.
	Temperature float64
}

// Response is the provider-agnostic completion result. Token counts are 0
// when the provider doesn't report them.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
}
