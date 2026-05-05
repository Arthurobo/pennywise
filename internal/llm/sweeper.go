package llm

import (
	"context"
	"log/slog"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// StartLogSweeper deletes llm_call_log rows older than retention. Runs
// hourly. Mirrors the sessions sweeper in internal/auth/session.go.
func StartLogSweeper(ctx context.Context, q *sqlcgen.Queries, retention time.Duration) {
	if retention <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				cutoff := time.Now().Add(-retention).UTC().Unix()
				n, err := q.DeleteOldLLMCallLog(ctx, cutoff)
				if err != nil {
					slog.Warn("llm log sweeper", "error", err)
					continue
				}
				if n > 0 {
					slog.Debug("llm log sweeper", "deleted", n)
				}
			}
		}
	}()
}
