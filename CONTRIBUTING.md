# Contributing to Pennywise

Thanks for your interest. Pennywise is a small, focused project; the bar for
"yes, let's add this" is high. The roadmap is in
[`docs/architecture.md`](docs/architecture.md), and explicit non-goals for v1
are in the original BUILD PROMPT.

## Dev setup

You need Go 1.22+ and `bash`. Everything else gets installed on demand.

```sh
git clone https://github.com/Arthurobo/pennywise.git
cd pennywise

# Tools installed via `go install` land in $(go env GOPATH)/bin
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2

# Tailwind standalone CLI is downloaded automatically by scripts/tailwind.sh
make tailwind
```

## Run in dev mode

```sh
make build      # compiles Tailwind CSS + the Go binary
./pennywise     # auto-detects you're in the repo, runs in dev mode
```

When `pennywise` starts inside a directory whose `go.mod` matches this
module's import path (`github.com/Arthurobo/pennywise`), it automatically:

- Sets `PENNYWISE_ENV=development` (templates reload from disk on every
  render so you don't need to rebuild after editing `.html` files)
- Defaults `PENNYWISE_DATA_DIR` to `./dev` (cwd-relative, gitignored) and
  `PENNYWISE_PORT` to `9003` (vs prod's `9002`), so
  the dev DB lives inside the repo and never collides with your real
  install at `~/.pennywise/pennywise.db`

The startup log shows `dev_auto_detected=true` whenever this kicks in.
Set `PENNYWISE_ENV=production` or `PENNYWISE_DATA_DIR=...` explicitly to
override. After Go code changes, `make build && ./pennywise`.

For the fast inner loop without rebuilding the binary every time:
```sh
go run ./cmd/pennywise
```

## Tests

```sh
make test               # go test -race -count=1 ./...
go test -run TestX ./...  # run one test
```

CI runs `golangci-lint` too — run `make lint` locally before opening a PR.

## Regenerating sqlc

If you edit anything in `internal/db/queries/*.sql` or
`internal/db/migrations/`, run:

```sh
make sqlc
```

CI fails if the generated code under `internal/db/sqlc/` is out of date.

## Code style notes

- Templates and static assets are served from `embed.FS` in production. In
  dev mode they reload from disk.
- Money is stored as integer minor units (cents). Never `float64`. See
  `internal/models/money.go`.
- `internal/handlers/*.go` is grouped by feature; the `Handler` struct hangs
  off every method.
- `internal/auth/` owns password hashing, sessions, CSRF, and middleware.
- New SQL queries go in `internal/db/queries/*.sql`. If sqlc emits
  `interface{}` for an aggregate column, wrap with `CAST(... AS INTEGER)`.

Avoid:

- ORMs. Stick with `sqlc` + hand-written SQL.
- New runtime dependencies (Postgres, Redis, etc.). The single-binary
  property is load-bearing.
- Non-Go build steps. Tailwind is the only exception, and it's a standalone
  binary downloaded by a bash script.

## Pull requests

- One topic per PR. Conventional commit prefixes preferred (`feat:`, `fix:`,
  `chore:`, `docs:`).
- Update `CHANGELOG.md` if user-visible behavior changes.
- For UI changes: include before/after screenshots in the PR description.
