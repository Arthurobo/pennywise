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

`PENNYWISE_ENV=development` reloads templates from disk on every render so you
don't need to rebuild after editing `.html` files.

```sh
make dev
# or:
PENNYWISE_ENV=development go run ./cmd/pennywise
```

The data directory defaults to `~/.pennywise`. Override with
`PENNYWISE_DATA_DIR=./data` if you want a project-local sandbox.

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
