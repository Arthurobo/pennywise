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

Visit `http://127.0.0.1:9002/setup` and fill in the same fields. After submit,
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

Once the owner is created, the recommended path is the OS service:

```sh
pennywise start    # install service + run; auto-starts at every login + reboot
pennywise status   # installed? running? PID, dashboard URL, logs
pennywise stop     # stop and remove from auto-start
```

`pennywise start` writes a service definition under your home directory
(no sudo needed) and registers it with the OS supervisor:

| Platform | Manager | Service definition |
|---|---|---|
| macOS | launchd | `~/Library/LaunchAgents/com.pennywise.app.plist` |
| Linux | systemd `--user` | `~/.config/systemd/user/pennywise.service` |
| Windows | Task Scheduler | task `Pennywise` (logon trigger, no admin needed) |

After install, Pennywise auto-starts at every reboot and self-restarts
on crash. Re-running `pennywise start` after a binary upgrade
(`go install ...@latest`) refreshes the service definition and restarts
the running process — no manual reload step.

Logs at `$PENNYWISE_DATA_DIR/pennywise.log`.

### Linux only: enable user-lingering for true reboot survival

`systemd --user` services run only while the user is interactively logged
in by default. To make Pennywise come back at boot before any login,
enable lingering once:

```sh
sudo loginctl enable-linger $USER
```

`pennywise start` prints a reminder if it detects lingering isn't enabled.

### Windows

`pennywise start` registers a Task Scheduler task that fires at user
logon, runs as the current user (no admin elevation required), and
restarts on failure. The task launches a `.bat` script in your data
dir that exports `PENNYWISE_*` env vars and runs `pennywise serve`,
redirecting stdout/stderr to `%USERPROFILE%\.pennywise\pennywise.log`.

`pennywise uninstall` removes the task, the launcher script, the data
directory, and the installed `.exe` (the `.exe` self-deletion happens
a moment after the command exits, since Windows locks running
executables).

### Foreground mode

```sh
pennywise          # blocks; Ctrl+C to stop
pennywise serve    # explicit form of the same
```

For development, debugging, or running under a different supervisor
(your own systemd unit, NSSM on Windows, etc.). Bypasses the OS service
install entirely.

## System-wide install (multi-user host)

The `pennywise start` flow installs a per-user service, which is the
right answer for personal laptops and single-user VPSes. For a shared
host where you want a system-wide daemon owned by a dedicated `pennywise`
user, use a traditional systemd unit:

```sh
sudo useradd --system --home /var/lib/pennywise --create-home pennywise
sudo cp ~/go/bin/pennywise /usr/local/bin/
```

Save as `/etc/systemd/system/pennywise.service`:

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
Environment=PENNYWISE_PORT=9002

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl enable --now pennywise
```

Then put nginx, Caddy, or Traefik in front for TLS.
