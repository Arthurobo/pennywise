# Installation

Two ways to install Pennywise.

## Option 1 — `go install` (recommended)

Anywhere with Go 1.25+ installed:

```sh
go install github.com/Arthurobo/pennywise/cmd/pennywise@latest
```

That drops the `pennywise` binary in `$GOBIN` (default `~/go/bin`). Make sure
that directory is on your `$PATH` and you can run `pennywise --help` from
anywhere.

Pennywise creates `~/.pennywise/` on first run. To pick a different location
use `PENNYWISE_DATA_DIR`. To bind to all interfaces (so you can reach it from
your LAN), set `PENNYWISE_HOST=0.0.0.0`. See
[`configuration.md`](configuration.md).

## Option 2 — Build from source

For contributors and anyone who wants to inspect or modify before running:

```sh
git clone https://github.com/Arthurobo/pennywise.git
cd pennywise
make build
./pennywise
```

You'll need Go 1.25+ and `bash` (the Tailwind CLI is downloaded automatically).
See [`CONTRIBUTING.md`](../CONTRIBUTING.md) for the full dev setup.

## First-run setup

After install, choose one of the two equivalent paths to create the singleton
owner account:

### Interactive CLI (recommended for headless installs)

```sh
./pennywise init
```

Walks through email, password, currency, and timezone, then writes the owner
row plus eight default categories. Refuses if Pennywise is already initialized
(use `./pennywise reset-password` to recover access).

This is the right choice for systemd one-shot units, scripted provisioning,
and any setup where launching a browser is awkward.

### In-browser

```sh
./pennywise        # starts the server
```

Visit `http://127.0.0.1:9001/setup` and fill in the same fields. After submit,
you're auto-logged-in and redirected to the dashboard.

Either path produces identical DB state. After the owner exists:

- The Telegram bot can be configured at *Settings → Telegram Bot*. See
  [`telegram.md`](telegram.md).
- The LLM provider can be configured at *Settings → LLM Provider*. See
  [`llm-providers.md`](llm-providers.md).
- The dashboard URL the bot mentions to users is editable at
  *Settings → Dashboard URL* — update it once you put Pennywise behind a real
  domain.

## Day-to-day: starting and stopping Pennywise

Once the owner is created, you have two ways to run the server:

### Foreground (development, systemd)

```sh
./pennywise          # blocks; Ctrl+C to stop
./pennywise serve    # explicit form of the same
```

This is the right mode under any process supervisor (systemd, launchd,
NSSM) — they handle lifecycle and just want a process to babysit.

### Background daemon (laptop / single-user installs)

For a personal laptop where you don't want a terminal pinned for the server:

```sh
./pennywise start    # forks into the background, writes a PID file,
                     # confirms /healthz responds before reporting success
./pennywise status   # PID, uptime, dashboard URL, log path
./pennywise stop     # graceful SIGTERM, escalates to SIGKILL after 10s
```

State files (Unix only):
- `$PENNYWISE_DATA_DIR/pennywise.pid` — PID + start timestamp
- `$PENNYWISE_DATA_DIR/pennywise.log` — combined stdout+stderr (append-mode)

`start` refuses if the PID in the file is still alive. Stale PID files
(file present but the process is gone — happens after `kill -9` or a hard
reboot) are detected by `status` and cleaned up by the next `start`.

The lifecycle commands are **Unix only** (Linux, macOS). On Windows, run
`pennywise serve` under Task Scheduler or NSSM instead.

**Reboot survival:** `pennywise start` does **not** auto-restart the
server when your machine reboots. For that, use one of the system-service
options below (systemd on Linux, launchd on macOS).

## Running as a system service (Linux, systemd)

Save this as `/etc/systemd/system/pennywise.service`:

```
[Unit]
Description=Pennywise expense tracker
After=network.target

[Service]
Type=simple
User=pennywise
ExecStart=/usr/local/bin/pennywise serve
Restart=on-failure
RestartSec=5s
Environment=PENNYWISE_DATA_DIR=/var/lib/pennywise
Environment=PENNYWISE_HOST=127.0.0.1
Environment=PENNYWISE_PORT=9001

[Install]
WantedBy=multi-user.target
```

```sh
sudo useradd --system --home /var/lib/pennywise --create-home pennywise
sudo cp pennywise /usr/local/bin/
sudo systemctl enable --now pennywise
```

Then put nginx, Caddy, or Traefik in front for TLS.
