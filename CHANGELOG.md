# Changelog

All notable changes to Pennywise are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- **v2.4 — operations & polish**
  - `pennywise start` / `stop` / `status` CLI commands (Unix only) for
    laptop deployments. `start` daemonizes via `setsid`, writes a PID
    file at `$DATA_DIR/pennywise.pid`, redirects logs to
    `$DATA_DIR/pennywise.log`, and polls `/healthz` for up to 5s before
    reporting success. `stop` sends SIGTERM with a 10s grace window
    before escalating to SIGKILL. `status` reports PID, uptime, dashboard
    URL, and detects stale PID files. Servers should still prefer
    systemd / launchd for crash + boot survival.
  - `pennywise init` CLI command for headless first-run setup (mirrors the
    web `/setup` form). Refuses if Pennywise is already initialized.
  - Confidence-aware confirmation in the Telegram bot: parses with
    confidence < 0.7 trigger a Yes / Edit / Cancel prompt instead of being
    silently logged. Threshold-zero values (no signal) auto-commit to avoid
    regressions. Batch parses always commit (with the existing Undo path).
  - Comprehensive docs: `docs/llm-providers.md`, `docs/telegram.md`,
    `docs/receipts.md`. README rewritten to cover v2 features. Three
    previously-undocumented env vars (`PENNYWISE_LLM_TIMEOUT_SECONDS`,
    `PENNYWISE_TELEGRAM_POLL_TIMEOUT_SECONDS`, `PENNYWISE_LLM_LOG_RETENTION_DAYS`)
    backfilled in `docs/configuration.md`.
  - Integration test suite: end-to-end Telegram dispatcher coverage
    (text / query / unclear / photo / vision-incapable / MIME mismatch /
    no-LLM-config) and `ParseReceipt` web handler tests using a shared
    `internal/testutil` package (`MockProvider`, `FakeTelegram`,
    `NewDB` / `SeedOwner` / `SeedDefaultCategories`).
- **v2.3 — receipt uploads**
  - Telegram bot accepts photos and image documents (JPG, PNG, WebP, HEIC,
    PDF). Per-provider MIME narrowing surfaces "your provider only accepts
    X" hints before the wire call.
  - Dashboard `/expenses/new` has a drop-zone above the form. Drag-drop or
    click-to-browse uploads a receipt; the form pre-fills via a JSON-only
    `POST /expenses/parse-receipt` endpoint.
  - All four catalog providers flagged vision-capable with verified per-
    provider format restrictions. xAI's `grok-4-1-fast-non-reasoning`
    confirmed JPG/PNG-only.
- **v2.2 — soft delete + bulk operations**
  - Soft-deleted expenses go to a Recently deleted view with restore /
    hard-delete / empty-trash actions. Per-owner `trash_retention_days`
    setting (default 30, max 365) drives an hourly purge sweeper.
  - Bulk select on the expenses table, bulk soft-delete via a floating
    action bar. Survives HTMX swaps via event delegation.
- **v2.1 — Telegram bot + LLM expense parsing**
  - BYOB Telegram bot setup (paste a BotFather token, generate pairing
    code, send `/start <code>`). Polling supervisor with auto-restart on
    config change.
  - Single unified LLM prompt covering expense parse, query intent, and
    unclear classification — one call per message, no regex fallback.
  - Four providers with one cheap-fast model each: OpenAI, Anthropic,
    Google Gemini, xAI Grok. AES-GCM-encrypted API key storage.
  - Per-owner `dashboard_url` setting surfaced in `/start`, `/help`, and
    the pairing-success message.
- **v1 — core**
  - First-run setup, login/logout, dashboard, expenses CRUD with filters,
    ledgers with budgets, categories management, reports with charts,
    CSV export, settings, CLI subcommands.
