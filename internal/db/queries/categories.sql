-- name: ListActiveCategories :many
SELECT id, name, color, icon, created_at, is_archived
FROM categories
WHERE is_archived = 0
ORDER BY name COLLATE NOCASE;

-- name: ListAllCategories :many
SELECT id, name, color, icon, created_at, is_archived
FROM categories
ORDER BY is_archived, name COLLATE NOCASE;

-- name: GetCategory :one
SELECT id, name, color, icon, created_at, is_archived
FROM categories
WHERE id = ?;

-- name: CreateCategory :one
INSERT INTO categories (name, color, icon, created_at, is_archived)
VALUES (?, ?, ?, ?, 0)
RETURNING id;

-- name: UpdateCategory :exec
UPDATE categories
SET name = ?, color = ?, icon = ?
WHERE id = ?;

-- name: SetCategoryArchived :exec
UPDATE categories
SET is_archived = ?
WHERE id = ?;

-- name: CategoryExpenseCount :one
-- Counts active (non-trashed) expenses only.
SELECT COUNT(*) FROM expenses WHERE category_id = ? AND deleted_at IS NULL;
