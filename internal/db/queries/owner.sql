-- name: GetOwner :one
-- Column order matches the table (post-migration: trash_retention_days
-- and dashboard_url were ALTER-appended last) so sqlc emits the canonical
-- Owner struct rather than a one-off GetOwnerRow.
SELECT id, email, password_hash, display_name, currency_code, currency_symbol, timezone,
       created_at, updated_at, trash_retention_days, dashboard_url
FROM owner
WHERE id = 1;

-- name: CreateOwner :exec
INSERT INTO owner (id, email, password_hash, display_name, currency_code, currency_symbol, timezone, created_at, updated_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateOwnerProfile :exec
UPDATE owner
SET email = ?, display_name = ?, updated_at = ?
WHERE id = 1;

-- name: UpdateOwnerPassword :exec
UPDATE owner
SET password_hash = ?, updated_at = ?
WHERE id = 1;

-- name: UpdateOwnerPreferences :exec
UPDATE owner
SET currency_code = ?, currency_symbol = ?, timezone = ?, updated_at = ?
WHERE id = 1;

-- name: UpdateOwnerTrashRetention :exec
UPDATE owner
SET trash_retention_days = ?, updated_at = ?
WHERE id = 1;

-- name: UpdateOwnerDashboardURL :exec
UPDATE owner
SET dashboard_url = ?, updated_at = ?
WHERE id = 1;
