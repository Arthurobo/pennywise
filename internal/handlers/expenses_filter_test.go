package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpenseFilter_NoFilters(t *testing.T) {
	// Soft-delete filter is always present, so the WHERE is never empty.
	f := ExpenseFilter{}
	where, args := f.where()
	assert.Equal(t, "WHERE e.deleted_at IS NULL", where)
	assert.Empty(t, args)
}

func TestExpenseFilter_AlwaysExcludesTrashed(t *testing.T) {
	// Trashed rows must never leak into the regular expenses list, regardless
	// of which optional filters are set.
	f := ExpenseFilter{From: 100, To: 200, Search: "x"}
	where, _ := f.where()
	assert.Contains(t, where, "e.deleted_at IS NULL")
}

func TestExpenseFilter_DateRange(t *testing.T) {
	f := ExpenseFilter{From: 100, To: 200}
	where, args := f.where()
	assert.True(t, strings.Contains(where, "spent_at >= ?"))
	assert.True(t, strings.Contains(where, "spent_at < ?"))
	assert.Equal(t, []any{int64(100), int64(200)}, args)
}

func TestExpenseFilter_Ledger(t *testing.T) {
	f := ExpenseFilter{LedgerID: 5}
	where, args := f.where()
	assert.Contains(t, where, "ledger_id = ?")
	assert.Equal(t, []any{int64(5)}, args)
}

func TestExpenseFilter_NoLedger(t *testing.T) {
	f := ExpenseFilter{LedgerID: -1}
	where, _ := f.where()
	assert.Contains(t, where, "ledger_id IS NULL")
}

func TestExpenseFilter_Search(t *testing.T) {
	f := ExpenseFilter{Search: "coffee"}
	where, args := f.where()
	assert.Contains(t, where, "description LIKE")
	assert.Contains(t, where, "notes")
	assert.Equal(t, []any{"%coffee%", "%coffee%"}, args)
}

func TestExpenseFilter_SearchEscapesWildcards(t *testing.T) {
	f := ExpenseFilter{Search: "100%_off"}
	_, args := f.where()
	assert.Equal(t, []any{`%100\%\_off%`, `%100\%\_off%`}, args)
}

func TestExpenseFilter_Combined(t *testing.T) {
	f := ExpenseFilter{
		From: 100, To: 200,
		LedgerID: 5, CategoryID: -1,
		Search:    "x",
		MinAmount: 1000, MaxAmount: 5000,
	}
	where, args := f.where()
	for _, want := range []string{
		"spent_at >= ?", "spent_at < ?",
		"ledger_id = ?", "category_id IS NULL",
		"description LIKE", "amount >= ?", "amount <= ?",
	} {
		assert.Contains(t, where, want, "missing %q in %s", want, where)
	}
	assert.Len(t, args, 7) // from, to, ledger, search ×2, min, max
}
