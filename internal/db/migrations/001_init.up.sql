-- Singleton owner row. CHECK constraint enforces single-tenant invariant.
CREATE TABLE owner (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    email           TEXT    NOT NULL UNIQUE,
    password_hash   TEXT    NOT NULL,
    display_name    TEXT    NOT NULL,
    currency_code   TEXT    NOT NULL DEFAULT 'USD',
    currency_symbol TEXT    NOT NULL DEFAULT '$',
    timezone        TEXT    NOT NULL DEFAULT 'UTC',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

-- App-wide flags (e.g. whether first-run setup has completed).
CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO app_state (key, value) VALUES ('initialized', 'false');

CREATE TABLE sessions (
    id          TEXT    PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    user_agent  TEXT,
    ip_address  TEXT
);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE categories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    color       TEXT    NOT NULL DEFAULT '#6b7280',
    icon        TEXT,
    created_at  INTEGER NOT NULL,
    is_archived INTEGER NOT NULL DEFAULT 0
);

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

CREATE TABLE expenses (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    amount      INTEGER NOT NULL,
    description TEXT    NOT NULL,
    notes       TEXT,
    spent_at    INTEGER NOT NULL,
    category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    ledger_id   INTEGER REFERENCES ledgers(id)    ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX idx_expenses_spent_at    ON expenses(spent_at);
CREATE INDEX idx_expenses_ledger_id   ON expenses(ledger_id);
CREATE INDEX idx_expenses_category_id ON expenses(category_id);
