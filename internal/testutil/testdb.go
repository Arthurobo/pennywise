// Package testutil provides shared test helpers: ephemeral SQLite databases
// with migrations applied, a MockProvider for the LLM interface, and a
// FakeTelegram httptest server that captures bot API calls. Production
// code never imports this package.
package testutil

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// NewDB returns a fresh *sql.DB with all migrations applied, on a temp file
// in t.TempDir(). The database is closed via t.Cleanup. Production-equivalent:
// WAL, foreign keys on, single-writer connection (matches db.Open).
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pennywise_test.db")
	db, err := pwdb.Open(path)
	if err != nil {
		t.Fatalf("testutil: open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// SeedOwner inserts the singleton owner row with usable defaults.
// Returns the freshly-loaded Owner so the test has the post-insert state
// (including the auto-defaulted trash_retention_days and dashboard_url).
func SeedOwner(t *testing.T, q *sqlcgen.Queries) sqlcgen.Owner {
	t.Helper()
	now := time.Now().UTC().Unix()
	if err := q.CreateOwner(context.Background(), sqlcgen.CreateOwnerParams{
		Email:          "test@pennywise.local",
		PasswordHash:   "$2a$12$placeholder.bcrypt.hash.never.verified.in.tests",
		DisplayName:    "Test Owner",
		CurrencyCode:   "USD",
		CurrencySymbol: "$",
		Timezone:       "UTC",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("testutil: create owner: %v", err)
	}
	owner, err := q.GetOwner(context.Background())
	if err != nil {
		t.Fatalf("testutil: re-read owner: %v", err)
	}
	return owner
}

// SeedDefaultCategories inserts the same 8 categories the production
// first-run setup creates: Food, Transport, Utilities, Housing, Health,
// Entertainment, Shopping, Other. Returns them in display order.
func SeedDefaultCategories(t *testing.T, q *sqlcgen.Queries) []sqlcgen.Category {
	t.Helper()
	specs := []struct {
		Name, Color string
	}{
		{"Food", "#f59e0b"},
		{"Transport", "#3b82f6"},
		{"Utilities", "#8b5cf6"},
		{"Housing", "#ef4444"},
		{"Health", "#10b981"},
		{"Entertainment", "#ec4899"},
		{"Shopping", "#06b6d4"},
		{"Other", "#6b7280"},
	}
	now := time.Now().UTC().Unix()
	for _, s := range specs {
		if _, err := q.CreateCategory(context.Background(), sqlcgen.CreateCategoryParams{
			Name: s.Name, Color: s.Color, CreatedAt: now,
		}); err != nil {
			t.Fatalf("testutil: create category %s: %v", s.Name, err)
		}
	}
	cats, err := q.ListActiveCategories(context.Background())
	if err != nil {
		t.Fatalf("testutil: list categories: %v", err)
	}
	return cats
}

// SeedLLMConfig inserts a usable llm_config row with the given provider
// and model. The encrypted-key column gets a non-empty placeholder so
// LLMEngine doesn't bail with errLLMNotConfigured — but tests using a
// MockProvider override never decrypt this value.
func SeedLLMConfig(t *testing.T, q *sqlcgen.Queries, provider, model string) sqlcgen.LlmConfig {
	t.Helper()
	now := time.Now().UTC().Unix()
	if err := q.UpsertLLMConfig(context.Background(), sqlcgen.UpsertLLMConfigParams{
		Provider:        provider,
		TextModel:       model,
		ApiKeyEncrypted: sql.NullString{String: "placeholder-not-decrypted-in-tests", Valid: true},
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("testutil: upsert llm config: %v", err)
	}
	cfg, err := q.GetLLMConfig(context.Background())
	if err != nil {
		t.Fatalf("testutil: re-read llm config: %v", err)
	}
	return cfg
}

// SeedTelegramConfig inserts a usable telegram_config row paired to the
// given chat ID. The bot_token column gets a placeholder.
func SeedTelegramConfig(t *testing.T, q *sqlcgen.Queries, chatID int64) sqlcgen.TelegramConfig {
	t.Helper()
	now := time.Now().UTC().Unix()
	if err := q.UpsertTelegramBot(context.Background(), sqlcgen.UpsertTelegramBotParams{
		BotTokenEncrypted: "placeholder-not-decrypted-in-tests",
		BotUsername:       "test_bot",
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("testutil: upsert telegram bot: %v", err)
	}
	if err := q.SetTelegramChatID(context.Background(), sqlcgen.SetTelegramChatIDParams{
		ChatID:    sql.NullInt64{Int64: chatID, Valid: true},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("testutil: set chat id: %v", err)
	}
	cfg, err := q.GetTelegramConfig(context.Background())
	if err != nil {
		t.Fatalf("testutil: re-read telegram config: %v", err)
	}
	return cfg
}
