package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/setupseed"
)

// runInitWithStdin executes the init subcommand with the given stdin
// transcript and returns (combined-output, error). The caller is
// responsible for setting PENNYWISE_DATA_DIR via t.Setenv beforehand
// so config.Load points at an isolated temp dir.
func runInitWithStdin(t *testing.T, stdin string) (string, error) {
	t.Helper()
	cmd := newInitCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(stdin))
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// setupTestEnv installs a fresh PENNYWISE_DATA_DIR so config.Load + db.Open
// land in a per-test temp location. Returns the data dir path.
func setupTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PENNYWISE_DATA_DIR", dir)
	t.Setenv("PENNYWISE_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	return dir
}

func TestInitCLI_CompleteFlow(t *testing.T) {
	dir := setupTestEnv(t)
	transcript := strings.Join([]string{
		"alice@example.com",
		"Alice",
		"hunter2hunter",
		"hunter2hunter",
		"USD",
		"UTC",
		"",
	}, "\n")
	out, err := runInitWithStdin(t, transcript)
	if err != nil {
		t.Fatalf("runInit returned error: %v\nout=%s", err, out)
	}
	if !strings.Contains(out, "Setup complete") {
		t.Fatalf("expected success message; got %q", out)
	}

	db, err := pwdb.Open(filepath.Join(dir, "pennywise.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	q := sqlcgen.New(db)

	owner, err := q.GetOwner(context.Background())
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	if owner.Email != "alice@example.com" {
		t.Fatalf("owner email: got %q, want alice@example.com", owner.Email)
	}
	if owner.DisplayName != "Alice" {
		t.Fatalf("display name: got %q, want Alice", owner.DisplayName)
	}
	if owner.CurrencyCode != "USD" {
		t.Fatalf("currency code: got %q, want USD", owner.CurrencyCode)
	}

	cats, err := q.ListActiveCategories(context.Background())
	if err != nil {
		t.Fatalf("list cats: %v", err)
	}
	if len(cats) != 8 {
		t.Fatalf("default categories count: got %d, want 8", len(cats))
	}

	v, err := q.GetAppState(context.Background(), setupseed.AppStateInitializedKey)
	if err != nil || v != "true" {
		t.Fatalf("initialized flag: got %q (err=%v), want \"true\"", v, err)
	}
}

func TestInitCLI_RefusesIfAlreadyInitialized(t *testing.T) {
	setupTestEnv(t)
	transcript := strings.Join([]string{
		"alice@example.com", "Alice", "hunter2hunter", "hunter2hunter", "USD", "UTC", "",
	}, "\n")
	if _, err := runInitWithStdin(t, transcript); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	out, err := runInitWithStdin(t, transcript)
	if err == nil {
		t.Fatalf("second init should refuse; got success: %s", out)
	}
	if !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("expected 'already initialized' error; got %v", err)
	}
}

func TestInitCLI_MismatchedPasswords(t *testing.T) {
	setupTestEnv(t)
	transcript := strings.Join([]string{
		"alice@example.com",
		"Alice",
		"hunter2hunter",
		"different-password",
	}, "\n")
	_, err := runInitWithStdin(t, transcript)
	if err == nil {
		t.Fatalf("expected error on mismatched passwords")
	}
	if !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("expected 'do not match' error; got %v", err)
	}
}

func TestInitCLI_InvalidEmailRetries(t *testing.T) {
	setupTestEnv(t)
	// First three emails fail validation, the fourth would succeed —
	// but the validator caps at 3 attempts and returns an error.
	transcript := strings.Join([]string{
		"not-an-email",
		"still-not-an-email",
		"@@@",
	}, "\n")
	_, err := runInitWithStdin(t, transcript)
	if err == nil {
		t.Fatalf("expected error after 3 invalid email attempts")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation-failed error; got %v", err)
	}
}

// Quiet the unused-import linter when go test stripping prevents reach.
var _ = os.Setenv
