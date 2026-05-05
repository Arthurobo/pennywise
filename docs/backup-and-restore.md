# Backup and restore

Pennywise stores everything in `$PENNYWISE_DATA_DIR/pennywise.db`. SQLite is
configured with WAL journaling, so the database survives `kill -9` mid-write.

## What to back up

In your data directory you'll find:

```
pennywise.db        # the database
pennywise.db-wal    # write-ahead log (only during writes)
pennywise.db-shm    # shared-memory file (only when open)
secret.key          # session HMAC secret (0600). Lose this and CSRF cookies invalidate.
```

`secret.key` is regenerated on next startup if missing — you'll just see a
single CSRF rejection per active browser, then everything continues.

## Cold backup (simplest)

Stop the server, then copy the file:

```sh
systemctl stop pennywise   # or `pennywise stop`, or just kill the process
cp ~/.pennywise/pennywise.db ~/backups/pennywise-$(date +%F).db
systemctl start pennywise
```

## Hot backup (no downtime)

Use SQLite's online backup API via the `sqlite3` CLI, which holds a brief read
lock instead of stopping writes:

```sh
sqlite3 ~/.pennywise/pennywise.db ".backup ~/backups/pennywise-$(date +%F).db"
```

This works while the server is running and gives you a fully consistent copy.

For a continuous backup, run that command from cron:

```cron
0 3 * * * sqlite3 ~/.pennywise/pennywise.db ".backup ~/backups/pennywise-$(date +\%F).db"
```

## Restore

Stop the server, replace the file, restart:

```sh
systemctl stop pennywise
cp ~/backups/pennywise-2026-04-30.db ~/.pennywise/pennywise.db
# remove any leftover WAL/SHM from the previous instance
rm -f ~/.pennywise/pennywise.db-wal ~/.pennywise/pennywise.db-shm
systemctl start pennywise
```

## Migrating between hosts

The database file is portable across architectures (SQLite is endian-aware).
Copy `pennywise.db` to the new host's data directory and start the binary.
Optionally copy `secret.key` too if you want existing browser sessions to keep
working — otherwise everyone signs in once.

## CSV export

For data portability (or to drop into a spreadsheet), use `Settings → Data →
Export all expenses (CSV)`, or hit `/export/csv` directly with the same filter
parameters as the expenses list.
