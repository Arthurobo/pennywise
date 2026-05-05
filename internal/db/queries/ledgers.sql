-- name: ListActiveLedgers :many
SELECT id, name, description, budget_amount, start_date, end_date, is_archived, created_at, updated_at
FROM ledgers
WHERE is_archived = 0
ORDER BY name COLLATE NOCASE;

-- name: ListAllLedgers :many
SELECT id, name, description, budget_amount, start_date, end_date, is_archived, created_at, updated_at
FROM ledgers
ORDER BY is_archived, name COLLATE NOCASE;

-- name: ListArchivedLedgers :many
SELECT id, name, description, budget_amount, start_date, end_date, is_archived, created_at, updated_at
FROM ledgers
WHERE is_archived = 1
ORDER BY name COLLATE NOCASE;

-- name: GetLedger :one
SELECT id, name, description, budget_amount, start_date, end_date, is_archived, created_at, updated_at
FROM ledgers
WHERE id = ?;

-- name: CreateLedger :one
INSERT INTO ledgers (name, description, budget_amount, start_date, end_date, is_archived, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 0, ?, ?)
RETURNING id;

-- name: UpdateLedger :exec
UPDATE ledgers
SET name = ?, description = ?, budget_amount = ?, start_date = ?, end_date = ?, updated_at = ?
WHERE id = ?;

-- name: SetLedgerArchived :exec
UPDATE ledgers
SET is_archived = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteLedger :exec
DELETE FROM ledgers WHERE id = ?;

-- name: LedgerExpenseCount :one
-- Counts active (non-trashed) expenses only.
SELECT COUNT(*) FROM expenses WHERE ledger_id = ? AND deleted_at IS NULL;

-- name: LedgerTotalSpent :one
-- Sums active (non-trashed) expenses only.
SELECT CAST(COALESCE(SUM(amount), 0) AS INTEGER) FROM expenses
WHERE ledger_id = ? AND deleted_at IS NULL;

-- name: ClearLedgerOnExpenses :exec
UPDATE expenses SET ledger_id = NULL, updated_at = ? WHERE ledger_id = ?;
