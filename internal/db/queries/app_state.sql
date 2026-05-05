-- name: GetAppState :one
SELECT value FROM app_state WHERE key = ?;

-- name: SetAppState :exec
INSERT INTO app_state (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;
