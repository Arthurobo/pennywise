-- name: GetLLMConfig :one
SELECT id, provider, api_key_encrypted, text_model, enabled,
       last_test_at, last_test_success, last_test_error,
       created_at, updated_at
FROM llm_config
WHERE id = 1;

-- name: UpsertLLMConfig :exec
INSERT INTO llm_config (
    id, provider, api_key_encrypted, text_model, enabled,
    last_test_at, last_test_success, last_test_error, created_at, updated_at
) VALUES (1, ?, ?, ?, 0, NULL, 0, NULL, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    provider          = excluded.provider,
    api_key_encrypted = COALESCE(excluded.api_key_encrypted, llm_config.api_key_encrypted),
    text_model        = excluded.text_model,
    updated_at        = excluded.updated_at;

-- name: SetLLMEnabled :exec
UPDATE llm_config SET enabled = ?, updated_at = ? WHERE id = 1;

-- name: UpdateLLMTestResult :exec
UPDATE llm_config
SET last_test_at      = ?,
    last_test_success = ?,
    last_test_error   = ?,
    updated_at        = ?
WHERE id = 1;

-- name: DeleteLLMConfig :exec
DELETE FROM llm_config WHERE id = 1;
