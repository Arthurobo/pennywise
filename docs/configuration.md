# Configuration

Pennywise reads everything from environment variables and applies sensible
defaults. There are no config files in v1.

| Variable                                  | Default                | Notes                                                               |
|-------------------------------------------|------------------------|---------------------------------------------------------------------|
| `PENNYWISE_DATA_DIR`                      | `~/.pennywise`         | Where `pennywise.db` and `secret.key` live. Created if missing.     |
| `PENNYWISE_HOST`                          | `127.0.0.1`            | Bind address. Set to `0.0.0.0` to listen on all interfaces.         |
| `PENNYWISE_PORT`                          | `9001`                 | HTTP port.                                                          |
| `PENNYWISE_SESSION_SECRET`                | (auto-generated)       | Hex-encoded, ≥32 hex chars. If unset, written to `$DATA_DIR/secret.key` (`0600`) on first run. |
| `PENNYWISE_LOG_LEVEL`                     | `info`                 | `debug`, `info`, `warn`, `error`.                                   |
| `PENNYWISE_ENV`                           | `production`           | `development` reloads templates from disk on every request.         |
| `PENNYWISE_LLM_TIMEOUT_SECONDS`           | `30`                   | Hard timeout per LLM call. Affects both bot replies and dashboard receipt uploads. |
| `PENNYWISE_TELEGRAM_POLL_TIMEOUT_SECONDS` | `30`                   | Long-poll timeout for the Telegram bot's getUpdates loop.           |
| `PENNYWISE_LLM_LOG_RETENTION_DAYS`        | `30`                   | How long rows in the `llm_call_log` table are kept. Maintenance sweeper purges older entries hourly. |

## Settings stored in the database (not env)

Some operational knobs live on the owner row instead of in env vars, so a user
on a hosted install can change them without shell access:

- **Dashboard URL** (`owner.dashboard_url`): the public address Pennywise is
  reachable at. Surfaced in the Telegram bot's `/start` and `/help` replies so
  the user always knows where to log in. Set during first-run setup to match
  whatever `PENNYWISE_HOST:PENNYWISE_PORT` resolved to; edit it in
  *Settings → Dashboard URL* once you put the app behind a real domain.
- **Trash retention** (`owner.trash_retention_days`): how long soft-deleted
  expenses are kept before the maintenance sweeper purges them. Default 30,
  cap 365. Editable in *Settings → Trash retention*.
- **Currency, currency symbol, timezone**: per-owner preferences set during
  setup, editable in *Settings → Preferences*.
- **LLM provider, model, encrypted API key**: stored in `llm_config`. Set via
  *Settings → LLM Provider*; the API key is AES-GCM-sealed at rest with a key
  derived from `PENNYWISE_SESSION_SECRET`.
- **Telegram bot token, chat ID**: stored in `telegram_config`, also encrypted.
  Set via *Settings → Telegram Bot*.

## Bind address

`127.0.0.1` is the default so a fresh install isn't accidentally exposed to the
network. To run behind a reverse proxy on the same host, leave it as is. To
serve directly to your LAN, set `PENNYWISE_HOST=0.0.0.0` and put TLS in front
of it (Pennywise terminates plain HTTP only).

## Session secret

The session cookie is opaque (random session ID looked up in the DB), but the
CSRF token derives from `HMAC(secret, csrf_id)`. Rotating the secret
invalidates every existing CSRF cookie — users will see one CSRF rejection on
their next form submit, then continue normally.

If you want full control, generate one and pin it:

```sh
PENNYWISE_SESSION_SECRET=$(openssl rand -hex 32) ./pennywise
```

## TLS

Pennywise speaks HTTP only. For internet exposure, terminate TLS in front:

- **Caddy** (zero config):
  ```
  pennywise.example.com {
    reverse_proxy 127.0.0.1:9001
  }
  ```
- **nginx**: standard `proxy_pass` to `http://127.0.0.1:9001`. Make sure to
  forward `X-Forwarded-Proto: https` so cookies set the `Secure` flag.

## Reset password from the CLI

```sh
./pennywise reset-password
```

Prompts for a new password (no echo) and revokes every active session.
