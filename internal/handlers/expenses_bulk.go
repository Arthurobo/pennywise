package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// BulkSoftDeleteExpenses soft-deletes every active expense whose ID is in ids.
// Returns the number of rows affected.
//
// Implemented with a built `IN (?, ?, …)` clause rather than via sqlc because
// sqlc's slice-param support requires a separate generator pass per length;
// this keeps everything in one round-trip regardless of selection size. The
// IDs go through `?` placeholders, never string concatenation, so injection
// isn't possible.
func BulkSoftDeleteExpenses(ctx context.Context, db *sql.DB, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	now := time.Now().UTC().Unix()
	args = append(args, now, now)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	q := fmt.Sprintf(
		`UPDATE expenses SET deleted_at = ?, updated_at = ?
         WHERE deleted_at IS NULL AND id IN (%s)`,
		strings.Join(placeholders, ","),
	) //nolint:gosec // placeholders are ?-only; user values flow through args
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
