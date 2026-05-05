DROP INDEX IF EXISTS idx_expenses_deleted_at;
ALTER TABLE expenses DROP COLUMN deleted_at;
ALTER TABLE owner DROP COLUMN trash_retention_days;
