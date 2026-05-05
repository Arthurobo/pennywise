-- Single-row table holding the user's LLM provider configuration.
-- API key is encrypted at rest with the AES-GCM secret box.
CREATE TABLE llm_config (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    provider            TEXT    NOT NULL,
    api_key_encrypted   TEXT,
    base_url            TEXT,
    text_model          TEXT    NOT NULL,
    enabled             INTEGER NOT NULL DEFAULT 0,
    last_test_at        INTEGER,
    last_test_success   INTEGER NOT NULL DEFAULT 0,
    last_test_error     TEXT,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
