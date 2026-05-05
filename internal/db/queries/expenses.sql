-- name: CreateExpense :one
INSERT INTO expenses (amount, description, notes, spent_at, category_id, ledger_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetExpense :one
-- Returns only active rows. Trash UI uses GetDeletedExpense for the trash side.
SELECT id, amount, description, notes, spent_at, category_id, ledger_id, created_at, updated_at, deleted_at
FROM expenses
WHERE id = ? AND deleted_at IS NULL;

-- name: GetDeletedExpense :one
-- Trash-only fetch: returns the row regardless of state, used by Restore /
-- HardDelete handlers that need to read a soft-deleted row.
SELECT id, amount, description, notes, spent_at, category_id, ledger_id, created_at, updated_at, deleted_at
FROM expenses
WHERE id = ?;

-- name: UpdateExpense :exec
UPDATE expenses
SET amount = ?, description = ?, notes = ?, spent_at = ?, category_id = ?, ledger_id = ?, updated_at = ?
WHERE id = ? AND deleted_at IS NULL;

-- name: SoftDeleteExpense :exec
-- Moves the row to trash. Idempotent: deleting an already-deleted row is a no-op.
UPDATE expenses SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL;

-- name: RestoreExpense :exec
-- Brings a trashed row back. No-op for active rows.
UPDATE expenses SET deleted_at = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL;

-- name: HardDeleteExpense :exec
-- Permanent delete. Used by the trash UI's "Delete forever" and by the
-- maintenance sweeper. Restricted to already-trashed rows so a stray call
-- can't bypass the trash.
DELETE FROM expenses WHERE id = ? AND deleted_at IS NOT NULL;

-- name: PurgeOldDeletedExpenses :execrows
-- Hard-deletes any soft-deleted rows older than the cutoff. Backs the
-- trash sweeper.
DELETE FROM expenses WHERE deleted_at IS NOT NULL AND deleted_at < ?;

-- name: EmptyTrash :execrows
-- Hard-deletes every currently soft-deleted row.
DELETE FROM expenses WHERE deleted_at IS NOT NULL;

-- name: ListDeletedExpenses :many
-- Trash listing, newest-deleted first.
SELECT
    e.id, e.amount, e.description, e.notes, e.spent_at,
    e.category_id, e.ledger_id, e.created_at, e.updated_at, e.deleted_at,
    c.name AS category_name, c.color AS category_color,
    l.name AS ledger_name
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
LEFT JOIN ledgers    l ON l.id = e.ledger_id
WHERE e.deleted_at IS NOT NULL
ORDER BY e.deleted_at DESC, e.id DESC
LIMIT ? OFFSET ?;

-- name: CountDeletedExpenses :one
SELECT CAST(COUNT(*) AS INTEGER) FROM expenses WHERE deleted_at IS NOT NULL;

-- name: ListRecentExpenses :many
SELECT
    e.id, e.amount, e.description, e.notes, e.spent_at,
    e.category_id, e.ledger_id, e.created_at, e.updated_at,
    c.name AS category_name, c.color AS category_color,
    l.name AS ledger_name
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
LEFT JOIN ledgers    l ON l.id = e.ledger_id
WHERE e.deleted_at IS NULL
ORDER BY e.spent_at DESC, e.id DESC
LIMIT ?;

-- name: TotalSpentBetween :one
SELECT CAST(COALESCE(SUM(amount), 0) AS INTEGER) FROM expenses
WHERE spent_at >= ? AND spent_at < ? AND deleted_at IS NULL;

-- name: SummaryBetween :one
SELECT
    CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS total,
    CAST(COUNT(*) AS INTEGER)                  AS expense_count
FROM expenses
WHERE spent_at >= ? AND spent_at < ? AND deleted_at IS NULL;

-- name: DailySpendingBetween :many
SELECT
    CAST(strftime('%s', date(spent_at, 'unixepoch')) AS INTEGER) AS day,
    CAST(COALESCE(SUM(amount), 0) AS INTEGER)                    AS total
FROM expenses
WHERE spent_at >= ? AND spent_at < ? AND deleted_at IS NULL
GROUP BY day
ORDER BY day;

-- name: SpendingByCategoryBetween :many
SELECT
    e.category_id,
    COALESCE(c.name, 'Uncategorized')          AS category_name,
    COALESCE(c.color, '#6b7280')               AS category_color,
    CAST(COALESCE(SUM(e.amount), 0) AS INTEGER) AS total
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
WHERE e.spent_at >= ? AND e.spent_at < ? AND e.deleted_at IS NULL
GROUP BY e.category_id
ORDER BY total DESC;

-- name: SpendingByLedgerBetween :many
SELECT
    e.ledger_id,
    COALESCE(l.name, 'No ledger')              AS ledger_name,
    CAST(COALESCE(SUM(e.amount), 0) AS INTEGER) AS total
FROM expenses e
LEFT JOIN ledgers l ON l.id = e.ledger_id
WHERE e.spent_at >= ? AND e.spent_at < ? AND e.deleted_at IS NULL
GROUP BY e.ledger_id
ORDER BY total DESC;

-- name: TopExpensesBetween :many
SELECT
    e.id, e.amount, e.description, e.spent_at,
    c.name AS category_name, c.color AS category_color,
    l.name AS ledger_name
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
LEFT JOIN ledgers    l ON l.id = e.ledger_id
WHERE e.spent_at >= ? AND e.spent_at < ? AND e.deleted_at IS NULL
ORDER BY e.amount DESC
LIMIT ?;

-- name: ListExpensesByLedger :many
SELECT
    e.id, e.amount, e.description, e.notes, e.spent_at,
    e.category_id, e.ledger_id, e.created_at, e.updated_at,
    c.name AS category_name, c.color AS category_color
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
WHERE e.ledger_id = ? AND e.deleted_at IS NULL
ORDER BY e.spent_at DESC, e.id DESC;

-- name: SpendingByCategoryForLedger :many
SELECT
    e.category_id,
    COALESCE(c.name, 'Uncategorized')          AS category_name,
    COALESCE(c.color, '#6b7280')               AS category_color,
    CAST(COALESCE(SUM(e.amount), 0) AS INTEGER) AS total
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
WHERE e.ledger_id = ? AND e.deleted_at IS NULL
GROUP BY e.category_id
ORDER BY total DESC;

-- name: DailySpendingForLedger :many
SELECT
    CAST(strftime('%s', date(spent_at, 'unixepoch')) AS INTEGER) AS day,
    CAST(COALESCE(SUM(amount), 0) AS INTEGER)                    AS total
FROM expenses
WHERE ledger_id = ? AND deleted_at IS NULL
GROUP BY day
ORDER BY day;
