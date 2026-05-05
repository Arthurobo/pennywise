package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ExpenseFilter is the parsed, validated filter set from query params.
type ExpenseFilter struct {
	From       int64 // unix; 0 = no lower bound
	To         int64 // unix; 0 = no upper bound
	LedgerID   int64 // 0 = any; -1 = none
	CategoryID int64 // 0 = any; -1 = none
	Search     string
	MinAmount  int64 // 0 = no min
	MaxAmount  int64 // 0 = no max

	Limit  int
	Offset int
}

// FilteredExpense is one row of the listing.
type FilteredExpense struct {
	ID            int64
	Amount        int64
	Description   string
	Notes         sql.NullString
	SpentAt       int64
	CategoryID    sql.NullInt64
	LedgerID      sql.NullInt64
	CategoryName  sql.NullString
	CategoryColor sql.NullString
	LedgerName    sql.NullString
}

// ListFilteredExpenses runs the dynamic listing query.
func ListFilteredExpenses(ctx context.Context, db *sql.DB, f ExpenseFilter) (rows []FilteredExpense, total int64, err error) {
	where, args := f.where()

	countSQL := "SELECT COUNT(*) FROM expenses e " + where
	if err := db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count expenses: %w", err)
	}

	// where is composed only of constant fragments + `?` placeholders; user
	// values flow through args. SQL injection is not possible here.
	const selectClause = `SELECT
        e.id, e.amount, e.description, e.notes, e.spent_at,
        e.category_id, e.ledger_id,
        c.name, c.color,
        l.name
    FROM expenses e
    LEFT JOIN categories c ON c.id = e.category_id
    LEFT JOIN ledgers    l ON l.id = e.ledger_id `
	const orderClause = ` ORDER BY e.spent_at DESC, e.id DESC LIMIT ? OFFSET ?`
	listSQL := selectClause + where + orderClause //nolint:gosec // see G202 note above
	listArgs := append(append([]any{}, args...), f.Limit, f.Offset)

	r, err := db.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list expenses: %w", err)
	}
	defer func() { _ = r.Close() }()
	for r.Next() {
		var x FilteredExpense
		if err := r.Scan(
			&x.ID, &x.Amount, &x.Description, &x.Notes, &x.SpentAt,
			&x.CategoryID, &x.LedgerID,
			&x.CategoryName, &x.CategoryColor,
			&x.LedgerName,
		); err != nil {
			return nil, 0, fmt.Errorf("scan expense: %w", err)
		}
		rows = append(rows, x)
	}
	if err := r.Err(); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// SumFilteredExpenses returns the total amount across all matching rows
// (ignoring pagination), so the listing can show "filtered total".
func SumFilteredExpenses(ctx context.Context, db *sql.DB, f ExpenseFilter) (int64, error) {
	where, args := f.where()
	var sum sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT SUM(amount) FROM expenses e "+where, args...).Scan(&sum); err != nil {
		return 0, fmt.Errorf("sum expenses: %w", err)
	}
	if sum.Valid {
		return sum.Int64, nil
	}
	return 0, nil
}

// where assembles the SQL fragment and bind args from the filter.
//
// Always includes `e.deleted_at IS NULL` — the expenses-list view never
// shows trashed rows; those live in the dedicated trash page.
func (f ExpenseFilter) where() (string, []any) {
	clauses := []string{"e.deleted_at IS NULL"}
	var args []any

	if f.From > 0 {
		clauses = append(clauses, "e.spent_at >= ?")
		args = append(args, f.From)
	}
	if f.To > 0 {
		clauses = append(clauses, "e.spent_at < ?")
		args = append(args, f.To)
	}
	switch {
	case f.LedgerID > 0:
		clauses = append(clauses, "e.ledger_id = ?")
		args = append(args, f.LedgerID)
	case f.LedgerID == -1:
		clauses = append(clauses, "e.ledger_id IS NULL")
	}
	switch {
	case f.CategoryID > 0:
		clauses = append(clauses, "e.category_id = ?")
		args = append(args, f.CategoryID)
	case f.CategoryID == -1:
		clauses = append(clauses, "e.category_id IS NULL")
	}
	if f.Search != "" {
		clauses = append(clauses, "(e.description LIKE ? OR COALESCE(e.notes, '') LIKE ?)")
		like := "%" + escapeLike(f.Search) + "%"
		args = append(args, like, like)
	}
	if f.MinAmount > 0 {
		clauses = append(clauses, "e.amount >= ?")
		args = append(args, f.MinAmount)
	}
	if f.MaxAmount > 0 {
		clauses = append(clauses, "e.amount <= ?")
		args = append(args, f.MaxAmount)
	}

	// clauses is never empty (deleted_at IS NULL is always present).
	return "WHERE " + strings.Join(clauses, " AND "), args
}

// escapeLike escapes %, _, and \ in the user-supplied search term so they
// match literally rather than as wildcards.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
