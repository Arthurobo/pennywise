package llm

// Model is one row in the catalog shown by the settings UI.
//
// Vision indicates whether the model accepts image inputs. The image-upload
// feature gates on this — when the user's currently-selected model has
// Vision=false, the receipt-upload UI is disabled with a hint to switch
// providers. Verified per-vendor docs at the same revision as the catalog
// entries below.
type Model struct {
	ID          string
	DisplayName string
	Notes       string
	Vision      bool
}

// ProviderInfo is what the settings dropdown renders.
type ProviderInfo struct {
	Key         string // value stored in llm_config.provider
	Name        string // human-readable label
	DocsURL     string // points to the LLM Providers docs section
	APIKeyLabel string // helper text under the API Key input
}

// ProvidersInOrder is the canonical display order for the provider dropdown.
var ProvidersInOrder = []ProviderInfo{
	{
		Key:         "openai",
		Name:        "OpenAI",
		DocsURL:     "/docs/llm-providers#openai",
		APIKeyLabel: "Get a key at platform.openai.com → API keys.",
	},
	{
		Key:         "anthropic",
		Name:        "Anthropic (Claude)",
		DocsURL:     "/docs/llm-providers#anthropic",
		APIKeyLabel: "Get a key at console.anthropic.com → API Keys.",
	},
	{
		Key:         "gemini",
		Name:        "Google Gemini",
		DocsURL:     "/docs/llm-providers#gemini",
		APIKeyLabel: "Get a key at aistudio.google.com → Get API key.",
	},
	{
		Key:         "xai",
		Name:        "xAI (Grok)",
		DocsURL:     "/docs/llm-providers#xai",
		APIKeyLabel: "Get a key at console.x.ai → API Keys.",
	},
}

// Catalog is the per-provider list of supported models.
//
// Pennywise's parsing workload — short JSON-output classification with a
// modest prompt — is a poor fit for premium reasoning models. The catalog
// intentionally exposes ONE model per provider: the cheapest, fastest,
// non-reasoning option each vendor sells. Speed and per-call cost matter
// more than ceiling intelligence here; the dispatcher already does the
// heavy lifting (prompt design, defensive parsing, fuzzy matching).
//
// Adding more models later is a one-line append per provider. The schema
// is `[]Model` per provider precisely so this stays trivial.
//
// First entry of each slice is the default. The settings UI shows the full
// list, the dispatcher uses whatever the user picked.
//
// Verified against vendor documentation in May 2026:
//   - OpenAI:    https://platform.openai.com/docs/models
//   - Anthropic: https://platform.claude.com/docs/en/about-claude/models/overview
//   - Gemini:    https://ai.google.dev/gemini-api/docs/models
//   - xAI:       https://docs.x.ai/developers/models
//
// Refresh each Pennywise release. Models flagged "preview" are listed only
// when no GA equivalent exists at the cheap-fast tier.
var Catalog = map[string][]Model{
	"openai": {
		// gpt-5.4-nano is the cheapest non-reasoning model in the GPT-5
		// line — purpose-built for "high-volume, simple tasks". Direct
		// successor to the role gpt-4o-mini used to fill before the
		// February 2026 retirement of the GPT-4o family.
		// Vision: yes — GPT-5 nano inherits multimodal input from the
		// 4o-mini lineage; receipts work directly.
		{ID: "gpt-5.4-nano", DisplayName: "GPT-5.4 Nano", Notes: "cheapest & fastest", Vision: true},
	},
	"anthropic": {
		// claude-haiku-4-5 is the current Haiku as of May 2026. There is
		// no 4.6 or 4.7 Haiku — Anthropic ships those numbers on Sonnet
		// (4.6) and Opus (4.7) only. Haiku 4.5 has near-frontier
		// intelligence at $1/MTok input — overkill for our parse.
		// Vision: yes — every Claude 3+ model accepts image content blocks.
		{ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", Notes: "cheapest & fastest", Vision: true},
	},
	"gemini": {
		// gemini-3.1-flash-lite-preview is the cheapest in the Gemini 3.x
		// line. It's preview tier, but Gemini 2.0 retires Jun 1, 2026 and
		// Gemini 2.5 Flash Lite is on the same deprecation path, so the
		// 3.1 preview is the durable cheap choice.
		// Vision: yes — the Flash family is natively multimodal.
		{ID: "gemini-3.1-flash-lite-preview", DisplayName: "Gemini 3.1 Flash Lite (preview)", Notes: "cheapest & fastest", Vision: true},
	},
	"xai": {
		// grok-4-1-fast-non-reasoning is xAI's cheapest non-reasoning
		// model in the Grok 4.1 family. The non-reasoning variant skips
		// the internal chain-of-thought — lower latency, lower cost,
		// plenty for short-text JSON classification.
		// Vision: yes — per xAI's docs (and Oracle's xAI mirror at
		// docs.oracle.com/.../xai-grok-4-1-fast.htm verified May 2026),
		// the non-reasoning variant accepts text + image input via the
		// OpenAI-compatible endpoint. The accepted formats are tighter
		// than the others (JPG/PNG only — no WebP/HEIC/PDF), enforced
		// per-provider via ProviderImageMIMETypes below.
		{ID: "grok-4-1-fast-non-reasoning", DisplayName: "Grok 4.1 Fast", Notes: "cheapest & fastest", Vision: true},
	},
}

// LookupProvider returns the catalog row for a key, or false.
func LookupProvider(key string) (ProviderInfo, bool) {
	for _, p := range ProvidersInOrder {
		if p.Key == key {
			return p, true
		}
	}
	return ProviderInfo{}, false
}

// DefaultModel returns the recommended model ID for a provider, or "".
func DefaultModel(providerKey string) string {
	models := Catalog[providerKey]
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}

// IsValidModel reports whether modelID is in the provider's catalog.
func IsValidModel(providerKey, modelID string) bool {
	for _, m := range Catalog[providerKey] {
		if m.ID == modelID {
			return true
		}
	}
	return false
}

// ModelSupportsVision reports whether the catalog entry for (providerKey,
// modelID) accepts image inputs.
//
// When modelID is unknown to the catalog but providerKey is known, we fall
// back to the provider's default-model Vision flag rather than failing
// closed. This handles catalog renames gracefully: if Pennywise updates
// the canonical xAI model ID, an install that hasn't re-saved its config
// still gets the right "vision-capable yes/no" answer based on the
// provider's intent. Unknown providers still fail closed.
func ModelSupportsVision(providerKey, modelID string) bool {
	models := Catalog[providerKey]
	if len(models) == 0 {
		return false
	}
	for _, m := range models {
		if m.ID == modelID {
			return m.Vision
		}
	}
	return models[0].Vision
}

// ProviderImageMIMETypes returns the set of MIME types the named provider
// will accept on image input. Pennywise's union (JPG/PNG/WebP/HEIC/PDF) is
// the outer envelope; this function narrows it per provider so users get a
// clear "your provider only accepts X" error before the API call lands.
//
// Returns nil for unknown providers — callers should treat that as "reject
// all image input" since the catalog is the source of truth.
//
// Verified against vendor docs (May 2026):
//   - OpenAI:    PNG, JPEG, WebP, GIF (non-animated treated as still).
//   - Anthropic: PNG, JPEG, WebP, GIF.
//   - Gemini:    PNG, JPEG, WebP, HEIC, HEIF, PDF (rasterized server-side).
//   - xAI:       PNG, JPEG only — narrower than the others.
func ProviderImageMIMETypes(providerKey string) map[string]bool {
	switch providerKey {
	case "openai":
		return map[string]bool{
			"image/jpeg": true,
			"image/png":  true,
			"image/webp": true,
			"image/gif":  true,
		}
	case "anthropic":
		return map[string]bool{
			"image/jpeg": true,
			"image/png":  true,
			"image/webp": true,
			"image/gif":  true,
		}
	case "gemini":
		return map[string]bool{
			"image/jpeg":      true,
			"image/png":       true,
			"image/webp":      true,
			"image/heic":      true,
			"image/heif":      true,
			"application/pdf": true,
		}
	case "xai":
		return map[string]bool{
			"image/jpeg": true,
			"image/png":  true,
		}
	}
	return nil
}
