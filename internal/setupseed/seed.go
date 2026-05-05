// Package setupseed contains the first-run owner+categories seed logic
// shared by the web /setup handler and the `pennywise init` CLI command.
//
// Both surfaces must produce identical DB state — same owner row shape,
// same default categories, same app_state flag — so they import this
// package rather than duplicating the SQL.
package setupseed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// AppStateInitializedKey is the key in the app_state table that gates
// first-run setup. Once it's "true", /setup is no longer reachable and
// `pennywise init` refuses to run.
const AppStateInitializedKey = "initialized"

// Category is one row in the seeded default-category list.
type Category struct {
	Name  string
	Color string
}

// DefaultCategories returns a fresh copy of the seeded categories. Returning
// a copy (rather than exposing a package-level slice) prevents callers from
// accidentally mutating the canonical definitions.
func DefaultCategories() []Category {
	return []Category{
		{"Food", "#f59e0b"},
		{"Transport", "#3b82f6"},
		{"Utilities", "#8b5cf6"},
		{"Housing", "#ef4444"},
		{"Health", "#10b981"},
		{"Entertainment", "#ec4899"},
		{"Shopping", "#06b6d4"},
		{"Other", "#6b7280"},
	}
}

// OwnerData is the input bundle. PasswordHash must already be bcrypt-hashed
// (callers use auth.Hash); the seeder doesn't hash, so callers can't
// accidentally re-hash an already-hashed value.
//
// DashboardURL is optional — when empty, the column DEFAULT from migration
// 007 applies. Callers should populate it from cfg.Addr() so the URL the
// Telegram bot advertises matches the port the binary actually binds to,
// regardless of any PENNYWISE_PORT override.
type OwnerData struct {
	Email          string
	PasswordHash   string
	DisplayName    string
	CurrencyCode   string
	CurrencySymbol string
	Timezone       string
	DashboardURL   string
}

// ErrAlreadyInitialized is returned by SeedInitialOwner when the
// app_state.initialized flag is already "true". Callers map this to the
// right surface-specific behavior — the web handler redirects, the CLI
// prints a hint about reset-password.
var ErrAlreadyInitialized = errors.New("setupseed: pennywise is already initialized")

// SeedInitialOwner creates the owner row, the 8 default categories, and
// flips app_state.initialized to "true" — all in a single transaction.
//
// Returns ErrAlreadyInitialized if a prior run completed (either via
// /setup or `pennywise init`). The caller is responsible for password
// hashing; OwnerData.PasswordHash must be the bcrypt output.
//
// On success, the caller should refresh any in-memory "initialized" flag
// (the web Handler does this via h.initialized.Store(true)).
func SeedInitialOwner(ctx context.Context, db *sql.DB, q *sqlcgen.Queries, data OwnerData) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("setupseed: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	tq := q.WithTx(tx)

	// Check for an existing owner first; the schema's CHECK(id=1) would
	// reject a second insert anyway, but a clean error is friendlier.
	if _, err := tq.GetOwner(ctx); err == nil {
		return ErrAlreadyInitialized
	} else if !errors.Is(err, sql.ErrNoRows) && err.Error() != "sql: no rows in result set" {
		return fmt.Errorf("setupseed: probe owner: %w", err)
	}

	now := time.Now().UTC().Unix()
	if err := tq.CreateOwner(ctx, sqlcgen.CreateOwnerParams{
		Email:          data.Email,
		PasswordHash:   data.PasswordHash,
		DisplayName:    data.DisplayName,
		CurrencyCode:   data.CurrencyCode,
		CurrencySymbol: data.CurrencySymbol,
		Timezone:       data.Timezone,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		return fmt.Errorf("setupseed: create owner: %w", err)
	}

	for _, c := range DefaultCategories() {
		if _, err := tq.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
			Name: c.Name, Color: c.Color, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("setupseed: create category %s: %w", c.Name, err)
		}
	}

	if data.DashboardURL != "" {
		if err := tq.UpdateOwnerDashboardURL(ctx, sqlcgen.UpdateOwnerDashboardURLParams{
			DashboardUrl: data.DashboardURL,
			UpdatedAt:    now,
		}); err != nil {
			return fmt.Errorf("setupseed: set dashboard url: %w", err)
		}
	}

	if err := tq.SetAppState(ctx, sqlcgen.SetAppStateParams{
		Key: AppStateInitializedKey, Value: "true",
	}); err != nil {
		return fmt.Errorf("setupseed: set initialized flag: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("setupseed: commit: %w", err)
	}
	return nil
}
