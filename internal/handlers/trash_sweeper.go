package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// StartTrashSweeper purges expense rows that have been in the trash longer
// than the owner's `trash_retention_days` setting. Runs hourly.
//
// We fetch the retention each tick (rather than caching it) so a settings
// change takes effect on the next sweep without restart. Cheap — the owner
// table has one row.
func StartTrashSweeper(ctx context.Context, q *sqlcgen.Queries) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		// Run once at startup so a long-running install doesn't wait an hour
		// after a config change.
		runTrashSweep(ctx, q)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runTrashSweep(ctx, q)
			}
		}
	}()
}

func runTrashSweep(ctx context.Context, q *sqlcgen.Queries) {
	owner, err := q.GetOwner(ctx)
	if err != nil {
		// First-run setup hasn't completed yet — nothing to do.
		return
	}
	if owner.TrashRetentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(owner.TrashRetentionDays) * 24 * time.Hour).UTC().Unix()
	n, err := q.PurgeOldDeletedExpenses(ctx, sql.NullInt64{Int64: cutoff, Valid: true})
	if err != nil {
		slog.Warn("trash sweeper", "error", err)
		return
	}
	if n > 0 {
		slog.Info("trash sweeper purged",
			"count", n,
			"retention_days", owner.TrashRetentionDays)
	}
}
