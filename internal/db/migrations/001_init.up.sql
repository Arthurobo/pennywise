-- Pennywise schema, consolidated.
--
-- This is the single migration that creates every table, index, and seed
-- row Pennywise needs. Earlier versions of the project shipped 8 numbered
-- migrations (001..008) covering the v1 → v2 evolution; that history was
-- squashed before the first public push so new installs start with a
-- clean schema_migrations table.
--
-- Layout:
--   owner            - singleton user row (CHECK id = 1 enforces it)
--   app_state        - simple key/value flags (e.g. "initialized")
--   sessions         - DB-backed session store
--   categories       - expense categories (archived, never deleted)
--   ledgers          - project / trip / budget envelopes
--   expenses         - the core data, with soft-delete via deleted_at
--   llm_config       - singleton LLM provider config (encrypted API key)
--   telegram_config  - singleton Telegram bot config (encrypted token)
--   llm_call_log     - append-only audit trail; trimmed by sweeper

-- ─── owner ─────────────────────────────────────────────────────────────────
CREATE TABLE owner (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    email                TEXT    NOT NULL UNIQUE,
    password_hash        TEXT    NOT NULL,
    display_name         TEXT    NOT NULL,
    currency_code        TEXT    NOT NULL DEFAULT 'USD',
    currency_symbol      TEXT    NOT NULL DEFAULT '$',
    timezone             TEXT    NOT NULL DEFAULT 'UTC',
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    trash_retention_days INTEGER NOT NULL DEFAULT 30,
    dashboard_url        TEXT    NOT NULL DEFAULT 'http://localhost:9002'
);

-- ─── app-wide flags ────────────────────────────────────────────────────────
CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO app_state (key, value) VALUES ('initialized', 'false');

-- ─── sessions ──────────────────────────────────────────────────────────────
CREATE TABLE sessions (
    id          TEXT    PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    user_agent  TEXT,
    ip_address  TEXT
);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

-- ─── categories ────────────────────────────────────────────────────────────
CREATE TABLE categories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    color       TEXT    NOT NULL DEFAULT '#6b7280',
    icon        TEXT,
    created_at  INTEGER NOT NULL,
    is_archived INTEGER NOT NULL DEFAULT 0
);

-- ─── ledgers ───────────────────────────────────────────────────────────────
CREATE TABLE ledgers (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL UNIQUE,
    description   TEXT,
    budget_amount INTEGER,
    start_date    INTEGER,
    end_date      INTEGER,
    is_archived   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- ─── expenses ──────────────────────────────────────────────────────────────
-- deleted_at is the unix timestamp of when the row was soft-deleted; NULL
-- means active. Maintenance sweeper hard-deletes rows where
-- deleted_at < (now - owner.trash_retention_days * 86400). The partial
-- index keeps the hot read path (deleted_at IS NULL) free of any size
-- penalty — it only indexes the small "currently in trash" set used by
-- the trash list and the sweeper.
CREATE TABLE expenses (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    amount      INTEGER NOT NULL,
    description TEXT    NOT NULL,
    notes       TEXT,
    spent_at    INTEGER NOT NULL,
    category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    ledger_id   INTEGER REFERENCES ledgers(id)    ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    deleted_at  INTEGER
);
CREATE INDEX idx_expenses_spent_at    ON expenses(spent_at);
CREATE INDEX idx_expenses_ledger_id   ON expenses(ledger_id);
CREATE INDEX idx_expenses_category_id ON expenses(category_id);
CREATE INDEX idx_expenses_deleted_at  ON expenses(deleted_at) WHERE deleted_at IS NOT NULL;

-- ─── llm_config ────────────────────────────────────────────────────────────
-- API key encrypted at rest with AES-GCM (key derived from
-- PENNYWISE_SESSION_SECRET via HKDF-SHA256). Each provider uses its
-- hardcoded vendor URL — no user-visible base_url override.
CREATE TABLE llm_config (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    provider            TEXT    NOT NULL,
    api_key_encrypted   TEXT,
    text_model          TEXT    NOT NULL,
    enabled             INTEGER NOT NULL DEFAULT 0,
    last_test_at        INTEGER,
    last_test_success   INTEGER NOT NULL DEFAULT 0,
    last_test_error     TEXT,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

-- ─── telegram_config ───────────────────────────────────────────────────────
-- Bot token encrypted at rest. chat_id stays NULL until pairing completes
-- via /start <code> in the bot.
CREATE TABLE telegram_config (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    bot_token_encrypted TEXT    NOT NULL,
    bot_username        TEXT    NOT NULL,
    chat_id             INTEGER,
    pairing_code        TEXT,
    pairing_expires_at  INTEGER,
    last_update_id      INTEGER NOT NULL DEFAULT 0,
    active_ledger_id    INTEGER REFERENCES ledgers(id) ON DELETE SET NULL,
    enabled             INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

-- ─── llm_call_log ──────────────────────────────────────────────────────────
-- Append-only audit trail. Maintenance sweeper trims rows older than
-- PENNYWISE_LLM_LOG_RETENTION_DAYS (default 30) on an hourly tick.
CREATE TABLE llm_call_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    provider        TEXT    NOT NULL,
    model           TEXT    NOT NULL,
    purpose         TEXT    NOT NULL,
    latency_ms      INTEGER NOT NULL,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    success         INTEGER NOT NULL,
    error_message   TEXT,
    created_at      INTEGER NOT NULL
);
CREATE INDEX idx_llm_call_log_created_at ON llm_call_log(created_at);
