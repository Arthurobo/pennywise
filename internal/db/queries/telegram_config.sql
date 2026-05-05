-- name: GetTelegramConfig :one
SELECT id, bot_token_encrypted, bot_username, chat_id, pairing_code,
       pairing_expires_at, last_update_id, active_ledger_id, enabled,
       created_at, updated_at
FROM telegram_config
WHERE id = 1;

-- name: UpsertTelegramBot :exec
-- Saving a bot token enables polling immediately so the bot can RECEIVE the
-- /start <pairing_code> message that completes the pairing. Without this,
-- the supervisor would never start the bot and pairing could never finish.
INSERT INTO telegram_config (
    id, bot_token_encrypted, bot_username, chat_id, pairing_code,
    pairing_expires_at, last_update_id, active_ledger_id, enabled,
    created_at, updated_at
) VALUES (1, ?, ?, NULL, NULL, NULL, 0, NULL, 1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    bot_token_encrypted = excluded.bot_token_encrypted,
    bot_username        = excluded.bot_username,
    chat_id             = NULL,
    pairing_code        = NULL,
    pairing_expires_at  = NULL,
    last_update_id      = 0,
    active_ledger_id    = NULL,
    enabled             = 1,
    updated_at          = excluded.updated_at;

-- name: SetTelegramPairingCode :exec
UPDATE telegram_config
SET pairing_code = ?, pairing_expires_at = ?, updated_at = ?
WHERE id = 1;

-- name: ClearTelegramPairingCode :exec
UPDATE telegram_config
SET pairing_code = NULL, pairing_expires_at = NULL, updated_at = ?
WHERE id = 1;

-- name: SetTelegramChatID :exec
UPDATE telegram_config
SET chat_id = ?, pairing_code = NULL, pairing_expires_at = NULL, enabled = 1, updated_at = ?
WHERE id = 1;

-- name: ClearTelegramChatID :exec
-- Disconnecting a chat keeps the bot polling so the user can re-pair from a
-- different Telegram account. The "Disable" button (SetTelegramEnabled) is
-- the only path that flips enabled to 0.
UPDATE telegram_config
SET chat_id = NULL, active_ledger_id = NULL, updated_at = ?
WHERE id = 1;

-- name: SetTelegramEnabled :exec
UPDATE telegram_config SET enabled = ?, updated_at = ? WHERE id = 1;

-- name: SetTelegramLastUpdateID :exec
UPDATE telegram_config SET last_update_id = ?, updated_at = ? WHERE id = 1;

-- name: SetTelegramActiveLedger :exec
UPDATE telegram_config SET active_ledger_id = ?, updated_at = ? WHERE id = 1;

-- name: DeleteTelegramConfig :exec
DELETE FROM telegram_config WHERE id = 1;
