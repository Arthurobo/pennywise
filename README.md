# Pennywise

> Local-first personal expense tracking. Your money, your machine.

Pennywise is a single-tenant, self-hosted expense tracker that runs as a single
static binary on your laptop, home server, Raspberry Pi, or NAS — in the same
spirit as Plex, Jellyfin, Linkwarden, or Home Assistant. One install, one user,
one SQLite file you can copy. No external services. No accounts somewhere else.

```
┌──────────────────────────────────────────────┐
│  Today        $24.50                         │
│  May 2026     $612.00                        │
│  2026         $4,127.50                      │
├──────────────────────────────────────────────┤
│  ▁▂▁▂▃▂▃▅▆▄▃▂▁▁▂▃▄▆▇▅▃▂▁▁▂▃▄▅▆ Last 30 days │
└──────────────────────────────────────────────┘
```

## Why Pennywise

- **Local-first.** All data lives in one SQLite file you control. Back it up by
  copying it. Move installs by copying it. Inspect it with `sqlite3` whenever.
- **Self-hosted, single-tenant.** No accounts on someone else's server, no
  subscriptions. One owner per install.
- **One binary, no runtime.** Pure-Go SQLite, embedded templates and assets.
  No Postgres, no Redis, no Node.js — one Go binary.
- **Optional LLM + Telegram, both BYOK.** Log expenses by texting a Telegram
  bot — *"5000 fuel"*, *"how much this month?"*, or even a snapshot of a
  receipt. Bring your own API key for OpenAI, Anthropic, Google Gemini, or xAI;
  Pennywise never proxies your traffic.

## Quick start

Anywhere with **Go 1.25+** installed (Linux, macOS, Windows, WSL, BSD):

```sh
go install github.com/Arthurobo/pennywise/cmd/pennywise@latest
pennywise init      # interactive: email, password, currency, timezone
pennywise start     # daemonizes; dashboard at http://127.0.0.1:9001
```

Then sign in at <http://127.0.0.1:9001/login>.

> `go install` drops the binary in `$GOBIN` (default `~/go/bin`). Make sure
> that directory is on your `$PATH` so `pennywise` is callable from anywhere.

Your data lives in `~/.pennywise/pennywise.db` by default; override via
[`PENNYWISE_DATA_DIR`](docs/configuration.md). The `start` command is Unix
only — Windows users run `pennywise serve` foreground or under Task
Scheduler / NSSM.

### Build from source

For contributors and anyone who'd rather inspect the code first:

```sh
git clone https://github.com/Arthurobo/pennywise.git
cd pennywise
make build
./pennywise init
./pennywise start
```

Requires Go 1.25+ and `bash` (the Tailwind CLI is downloaded on first build).

## Features

### Core (v1)

- Single-owner, password-protected install. Bcrypt + DB-backed sessions, CSRF
  on every form, HttpOnly secure cookies.
- Dashboard: today/month/year totals, last-30-days chart, recent expenses,
  active-ledgers grid with budget progress bars.
- Expenses: full CRUD, filtering by date / ledger / category / search /
  amount range, HTMX-powered swaps, **bulk select & bulk delete**.
- Soft delete with a [Recently deleted](docs/installation.md#first-run-setup)
  trash view and per-owner retention window (default 30 days, max 365).
- Ledgers (project / trip / budget envelopes) with detail page, breakdown
  charts, archive toggle.
- Categories with archive (not delete) so historical data stays linked.
- Reports: by-category and by-ledger breakdowns, time series, top-10 largest
  expenses.
- CSV export.
- All-currency picker (155 ISO 4217 codes), 30 common timezones.

### Optional add-ons (v2)

- **Telegram bot (BYOB — bring your own bot):** create a bot with @BotFather,
  paste the token in Settings, pair via `/start <code>`, then text expenses
  in plain language. See [docs/telegram.md](docs/telegram.md).
- **LLM expense parsing (BYOK — bring your own key):** OpenAI, Anthropic,
  Gemini, or xAI. One model per provider, all on the cheap-fast tier. See
  [docs/llm-providers.md](docs/llm-providers.md).
- **Receipt image uploads** in both the dashboard and the bot — drop a JPG
  or PDF receipt and Pennywise pre-fills (or logs) the expense. See
  [docs/receipts.md](docs/receipts.md).
- **Low-confidence prompts:** ambiguous parses ("paid 3000") trigger a
  Yes / Edit / Cancel confirmation in the bot before anything is logged.

## Tech stack

| Layer            | Choice                                         |
|------------------|------------------------------------------------|
| Language         | Go 1.25+                                       |
| HTTP router      | `github.com/go-chi/chi/v5`                     |
| Database         | SQLite (one file, embedded)                    |
| SQLite driver    | `modernc.org/sqlite` — pure Go, no CGO         |
| SQL → Go         | `sqlc` from hand-written queries               |
| Migrations       | `github.com/golang-migrate/migrate/v4`         |
| Templating       | `html/template` (stdlib), embedded             |
| Frontend         | HTMX 2.x + a tiny `app.js`                     |
| Charts           | Chart.js (vendored, not CDN)                   |
| CSS              | Tailwind via standalone CLI (no Node.js)       |
| CLI              | `github.com/spf13/cobra`                       |
| Password hashing | `golang.org/x/crypto/bcrypt`, cost 12          |
| Session storage  | One `sessions` table, custom manager           |
| LLM (optional)   | Direct REST calls — no SDK per provider        |
| Telegram (opt.)  | Direct Bot API calls — no SDK                  |

## CLI reference

```
./pennywise                  # run the server in the foreground (default)
./pennywise serve            # explicit form of the default
./pennywise init             # interactive first-run setup

# Background lifecycle (Unix only — laptops, single-user installs):
./pennywise start            # daemonize, write PID file, /healthz check
./pennywise stop             # graceful SIGTERM, escalates to SIGKILL after 10s
./pennywise status           # PID, uptime, dashboard URL, log path

./pennywise reset-password   # reset the owner password (revokes sessions)
./pennywise version          # print build info
```

DB migrations run **automatically** every time Pennywise opens the database —
on every `start`, `serve`, `init`, and `reset-password`. There's no separate
`migrate` command; you never need to think about schema versions.

For long-lived servers, prefer **systemd** (Linux) or **launchd** (macOS)
over `start`/`stop` — they restart on crash and at boot. The lifecycle
commands exist for laptop / single-user installs where setting up an init
system is overkill. See
[`docs/installation.md`](docs/installation.md#running-as-a-system-service-linux-systemd).

## Backup & restore

Everything is in `$PENNYWISE_DATA_DIR/pennywise.db`. To back up:

```sh
# Stop the server first for a perfectly consistent snapshot.
cp ~/.pennywise/pennywise.db ~/backups/pennywise-$(date +%F).db
```

WAL mode is on, so the DB survives `kill -9` mid-write. See
[`docs/backup-and-restore.md`](docs/backup-and-restore.md) for hot-backup.

## Documentation

- [Installation](docs/installation.md)
- [Configuration](docs/configuration.md)
- [LLM provider setup](docs/llm-providers.md)
- [Telegram bot setup](docs/telegram.md)
- [Receipt uploads](docs/receipts.md)
- [Backup and restore](docs/backup-and-restore.md)
- [Architecture](docs/architecture.md)

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). PRs welcome. Open an issue first for
anything bigger than a small fix.

## License

MIT. See [`LICENSE`](LICENSE).
