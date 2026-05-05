-- Single-row table for the Telegram bot binding. Bot token is encrypted at
-- rest with the AES-GCM secret box. The chat_id stays NULL until the user
-- pairs by sending /start <code>.
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
