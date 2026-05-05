package telegram

import (
	"context"
	"errors"
	"log/slog"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// poll is the long-polling loop. It calls getUpdates with a long timeout,
// processes each batch, and persists last_update_id BEFORE handling so a
// crash mid-batch doesn't reprocess (acceptable tradeoff: a single message
// might be dropped on crash; for a personal tracker that's fine).
func (b *Bot) poll(ctx context.Context) error {
	tg, err := b.q.GetTelegramConfig(ctx)
	if err != nil {
		return err
	}
	offset := tg.LastUpdateID + 1

	pollTimeout := b.pollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 30 * time.Second
	}

	backoff := time.Second
	const maxBackoff = 60 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		updates, err := b.client.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if errors.Is(err, ErrUnauthorized) {
				slog.Warn("telegram: 401 unauthorized — disabling bot")
				_ = b.q.SetTelegramEnabled(ctx, sqlcgen.SetTelegramEnabledParams{
					Enabled:   0,
					UpdatedAt: time.Now().UTC().Unix(),
				})
				return ErrUnauthorized
			}
			slog.Warn("telegram getUpdates failed", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second

		if len(updates) == 0 {
			continue
		}

		// Track the highest update_id and persist BEFORE handling, per spec.
		var highest int64
		for _, u := range updates {
			if u.UpdateID > highest {
				highest = u.UpdateID
			}
		}
		offset = highest + 1
		if err := b.q.SetTelegramLastUpdateID(ctx, sqlcgen.SetTelegramLastUpdateIDParams{
			LastUpdateID: highest,
			UpdatedAt:    time.Now().UTC().Unix(),
		}); err != nil {
			slog.Warn("telegram: persist last_update_id", "error", err)
		}

		for _, u := range updates {
			b.dispatcher.Dispatch(ctx, u)
		}
	}
}
