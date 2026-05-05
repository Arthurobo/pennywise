-- Per-owner dashboard URL.
--
-- This is the public address users hit to reach the web UI. It is surfaced
-- back to them in the Telegram bot (in /start, /help, and pairing-success
-- messages) so they always know where to log in.
--
-- The column DEFAULT is a fallback for rows inserted without an explicit
-- value. The setupseed package always passes the dynamic cfg.Addr() string
-- when creating the owner row, so this default never fires in normal flows.
ALTER TABLE owner ADD COLUMN dashboard_url TEXT NOT NULL DEFAULT 'http://localhost:9001';
