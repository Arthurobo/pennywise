-- Append-only log of every LLM call. The maintenance sweeper trims rows
-- older than PENNYWISE_LLM_LOG_RETENTION_DAYS (default 30).
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
