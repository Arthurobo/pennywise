-- name: CreateSession :exec
INSERT INTO sessions (id, created_at, expires_at, user_agent, ip_address)
VALUES (?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT id, created_at, expires_at, user_agent, ip_address
FROM sessions
WHERE id = ? AND expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ?;

-- name: DeleteAllSessions :exec
DELETE FROM sessions;
