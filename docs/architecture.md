# Architecture

Pennywise is intentionally small. This document describes the big-picture
shape so contributors can find their way around.

## Single-tenant by design

There is exactly one owner row, ever, enforced by a `CHECK (id = 1)` constraint
on the `owner` table. Data tables (`expenses`, `ledgers`, `categories`) carry
no `user_id` foreign key. There is no multi-user code path to add later.

If you find yourself wanting "share with my partner" or "team accounts," that's
a different product. Keep this one focused.

## Layout

```
cmd/pennywise/        Entry point — wires cobra and exits.
internal/
  auth/               bcrypt, sessions, CSRF, middleware.
  cli/                Cobra subcommands (serve, start/stop/status, init, reset-password, version).
  config/             Env-var loader; auto-generates secret.key on first run.
  db/
    db.go             SQLite open + migrations.
    migrations/       Numbered .up.sql / .down.sql files.
    queries/          Hand-written .sql files; sqlc generates code from these.
    sqlc/             Auto-generated; never hand-edit.
  handlers/           HTTP handlers grouped by feature. One Handler struct.
  models/             Money/currency/timezone helpers.
  server/             chi router setup and middleware chain.
  static/             embed.FS for /static (CSS, JS, etc.).
  templates/          embed.FS for HTML; Renderer parses each page against
                      the layout + partials.
```

## Request lifecycle

```
incoming
  → requestLogger      (one structured log line per request)
  → recoverer          (panics become 500s, never crashes the process)
  → secureHeaders      (CSP, X-Frame-Options, etc.)
  → methodOverride     (HTML forms with _method=DELETE/PUT/PATCH)
  → AttachCSRF         (mints csrf_id cookie if absent, computes token)
  → AttachSession      (looks up session, attaches Owner to context if any)
  → group (gated by setup state / auth)
    → VerifyCSRF       (rejects mutations without a valid token)
    → handler
```

Routes that need a logged-in owner are wrapped in `RequireAuth`, which redirects
to `/login?next=...` when no owner is in context. First-run setup uses two
inverse middlewares: `RequireSetup` (redirects to `/setup` until the owner
exists) and `RejectIfInitialized` (returns 404 from `/setup` once it does).

## Templates

Each page in `internal/templates/pages/*.html` is parsed against the base
layout (`layouts/base.html`) and every partial in `partials/*.html`. The
Renderer keeps a `map[pageName]*template.Template` registry. Pages override
`{{ define "content" }}` blocks; partials define standalone templates that
pages can include.

In dev mode (`PENNYWISE_ENV=development`) the registry is rebuilt on every
render so disk edits are visible without a rebuild.

## CSRF

Synchronizer-token-style with HMAC. A long-lived `pennywise_csrf` cookie carries
an opaque 32-byte identifier; the token is `HMAC(session_secret, identifier)`.
On a mutating request, the server recomputes the expected token from the
cookie and compares it (in constant time) to the value in the `_csrf` form
field or `X-CSRF-Token` header.

This avoids session-bound CSRF storage and survives session creation/destruction
cleanly. Logging out doesn't invalidate the CSRF cookie because the token
isn't tied to the session ID.

## Sessions

Random 32-byte hex IDs persisted in the `sessions` table with a 30-day expiry.
A goroutine sweeps expired rows hourly. Cookies are HttpOnly, SameSite=Lax,
and Secure when the request looks like HTTPS (`r.TLS != nil` or
`X-Forwarded-Proto: https`).

## Money

Stored as integer minor units (cents). The `models.ParseAmount` /
`FormatAmount` pair handles user-entered decimals. Aggregate SQL columns are
wrapped in `CAST(COALESCE(SUM(amount), 0) AS INTEGER)` so sqlc emits `int64`
return types instead of `interface{}`.

## Filtering expenses

`internal/handlers/expenses_filter.go` builds the dynamic `WHERE` clause
manually with `database/sql`. sqlc handles every static query; the dynamic
filter is the one place we step outside it. The filter also escapes user-
supplied wildcards (`%`, `_`, `\`) before substituting into a `LIKE` clause.

## Future-proofing for v2/v3

The spec calls out future bot integrations and a "Pennywise Connect" tunnel.
The architectural choices here don't preclude those:

- Handlers are pure HTTP — adding alternate transports (a Telegram bot, a
  WebSocket endpoint, an MCP server) is additive.
- The `Handler` struct holds all dependencies; shipping new entry points
  means constructing a new Handler against the same DB.
- The single-tenant assumption holds for those use cases too: a tunnel is
  one-to-one with one install.

What it would *not* survive is bolting on multi-user. That's the architectural
red line.
