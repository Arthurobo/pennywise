-- Soft delete on expenses + per-owner trash retention.
--
-- expenses.deleted_at is the unix timestamp of when the row was moved to
-- trash. NULL means active. The maintenance sweeper hard-deletes rows where
-- deleted_at < (now - owner.trash_retention_days * 86400).
--
-- A partial index keeps the hot read path (deleted_at IS NULL) free of any
-- size penalty — it only indexes the small "currently in trash" set used by
-- the trash list and the sweeper.
ALTER TABLE expenses ADD COLUMN deleted_at INTEGER;
CREATE INDEX idx_expenses_deleted_at ON expenses(deleted_at) WHERE deleted_at IS NOT NULL;

-- Per-owner trash retention window. Capped at 365 days in the application
-- layer; we don't enforce that in the schema since SQLite CHECK on ALTER
-- requires a table rebuild on older SQLite versions and the cap is a UX
-- decision, not a data-integrity one.
ALTER TABLE owner ADD COLUMN trash_retention_days INTEGER NOT NULL DEFAULT 30;
