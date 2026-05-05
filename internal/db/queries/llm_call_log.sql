-- name: InsertLLMCallLog :exec
INSERT INTO llm_call_log (
    provider, model, purpose, latency_ms, input_tokens, output_tokens,
    success, error_message, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteOldLLMCallLog :execrows
DELETE FROM llm_call_log WHERE created_at < ?;

-- name: RecentLLMCallLog :many
SELECT id, provider, model, purpose, latency_ms, input_tokens, output_tokens,
       success, error_message, created_at
FROM llm_call_log
ORDER BY created_at DESC
LIMIT ?;
