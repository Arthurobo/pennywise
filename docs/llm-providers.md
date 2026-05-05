# LLM provider setup

Pennywise's optional natural-language features (the Telegram bot, dashboard
receipt uploads) need an LLM provider. Pennywise is **BYOK — bring your own
API key**: traffic goes directly from your install to the vendor; Pennywise
never proxies it.

Four providers are supported, each exposing one cheap-fast model in the
catalog. Pick whichever you already pay for, or whichever has the lowest
per-call cost in your region.

| Provider     | Catalog model                       | Vision support | Notes                  |
|--------------|-------------------------------------|----------------|------------------------|
| OpenAI       | `gpt-5.4-nano`                      | ✓              | JPG, PNG, WebP, GIF   |
| Anthropic    | `claude-haiku-4-5`                  | ✓              | JPG, PNG, WebP, GIF   |
| Google       | `gemini-3.1-flash-lite-preview`     | ✓              | JPG, PNG, WebP, HEIC, PDF |
| xAI (Grok)   | `grok-4-1-fast-non-reasoning`       | ✓              | JPG, PNG only         |

The "vision support" column drives the receipt-upload feature — see
[`receipts.md`](receipts.md).

## Setup steps (any provider)

1. Open *Settings → LLM Provider* in Pennywise.
2. Pick your provider. The model dropdown auto-fills with the cheap-fast
   default.
3. Paste your API key.
4. Click **Save**, then **Test connection**. On success, the LLM is enabled
   immediately and the Telegram bot starts processing messages.

The API key is encrypted at rest with AES-GCM, using a key derived from
`PENNYWISE_SESSION_SECRET` via HKDF-SHA256. Rotating the session secret means
re-saving the API key once.

## Provider-specific instructions

### OpenAI

Get a key at <https://platform.openai.com> → *API keys*. The catalog model
is `gpt-5.4-nano`, OpenAI's cheapest non-reasoning model in the GPT-5 line —
direct successor to the role `gpt-4o-mini` used to fill before the GPT-4o
family was retired.

- Pricing: see <https://openai.com/api/pricing/>.
- Rate limits scale with your tier; for personal expense logging the free
  tier is more than enough.

### Anthropic (Claude)

Get a key at <https://console.anthropic.com> → *API Keys*. The catalog model
is `claude-haiku-4-5`. There's no Haiku 4.6 or 4.7 — Anthropic ships those
numbers on Sonnet (4.6) and Opus (4.7) only.

- Pricing: see <https://www.anthropic.com/pricing>.
- Anthropic doesn't have a "JSON mode" flag; Pennywise uses the documented
  assistant-prefill technique (`{`) to force valid JSON output. No setup
  on your end.

### Google Gemini

Get a key at <https://aistudio.google.com> → *Get API key*. The catalog model
is `gemini-3.1-flash-lite-preview`, the cheapest in the Gemini 3.x line.

- Pricing: see <https://ai.google.dev/pricing>.
- "Preview" tier is durable here — Gemini 2.0 retires June 1, 2026 and the
  2.5 Flash Lite is on the same path; the 3.1 preview is the long-term
  cheap-fast default.

### xAI (Grok)

Get a key at <https://console.x.ai> → *API Keys*. The catalog model is
`grok-4-1-fast-non-reasoning`. Despite the name, this variant **does** accept
image input — it just supports a narrower set of formats (JPG and PNG only,
not WebP / HEIC / PDF) than the other three providers.

- Pricing: see <https://docs.x.ai/docs/models>.
- xAI's Chat Completions API is OpenAI-compatible, so the wire format
  is identical.

## Common failure modes

**"LLM provider isn't set up or is failing."**
- Click *Test connection* in *Settings → LLM Provider*. If the key is wrong
  or expired, the test message will surface the vendor's error verbatim.
- Check the request log in `llm_call_log` (visible at *Settings → LLM Provider*
  under the connection-status block).

**"Image uploads need a vision-capable model."**
- All four catalog models support vision. If you see this message, the model
  ID stored in `llm_config` likely doesn't match the catalog (e.g. after a
  catalog rename). Re-save in *Settings → LLM Provider* — the dropdown will
  show the current catalog default.

**"Your provider doesn't accept image/webp."**
- xAI accepts only JPG and PNG. Re-export the receipt or switch providers.

**Rate limit / quota errors.**
- The bot retries automatically with exponential backoff for transient
  failures. Persistent quota errors mean your account is throttled — check
  your provider dashboard.

## Switching providers

Switching is non-destructive: changing the provider in *Settings → LLM Provider*
re-encrypts the new key under the same session secret. Existing parsed
expenses, the call log, and the bot pairing are unaffected.

If you change provider while the bot is processing a message, the next
message picks up the new provider — there's no caching.
