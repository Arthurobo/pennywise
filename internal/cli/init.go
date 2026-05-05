package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/config"
	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
	"github.com/Arthurobo/pennywise/internal/setupseed"
)

// newInitCommand returns the `pennywise init` subcommand.
//
// The command walks an interactive setup that mirrors the web /setup form
// — email, display name, password (with confirmation), currency, timezone
// — and writes the same DB rows. It refuses if first-run setup already
// completed, directing the user to `pennywise reset-password` instead.
//
// Designed for headless installs: systemd one-shot units, scripted
// provisioning, CI bootstraps. The web /setup flow remains fully
// supported for users who'd rather click through a browser.
func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Run interactive first-time setup (creates owner, default categories)",
		Long: `Walks through email, display name, password, currency, and timezone,
then writes the singleton owner row and the default category set. After init,
start the server with ./pennywise and log in at /login.

Refuses if Pennywise is already initialized — use ./pennywise reset-password
to recover access if you've forgotten your credentials.`,
		RunE: runInit,
	}
}

func runInit(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := pwdb.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	q := sqlcgen.New(db)

	// Refuse early if already initialized — gives a clear "use
	// reset-password" hint instead of dropping into a doomed prompt.
	if v, err := q.GetAppState(ctx, setupseed.AppStateInitializedKey); err == nil && v == "true" {
		return errors.New("Pennywise is already initialized. Use `./pennywise reset-password` if you've forgotten your login.")
	}

	out := cmd.OutOrStderr()
	in := bufio.NewReader(cmd.InOrStdin())
	fmt.Fprintln(out, "Welcome to Pennywise. Let's set up your account.")
	fmt.Fprintln(out, "")

	email, err := promptValidatedLine(out, in, "Email: ", validateEmail)
	if err != nil {
		return err
	}
	name, err := promptValidatedLine(out, in, "Display name: ", validateNonEmpty)
	if err != nil {
		return err
	}
	pwd, err := promptPasswordFrom(in, fmt.Sprintf("Password (min %d characters): ", auth.MinPasswordLength))
	if err != nil {
		return err
	}
	pwd2, err := promptPasswordFrom(in, "Confirm password: ")
	if err != nil {
		return err
	}
	if pwd != pwd2 {
		return errors.New("passwords do not match")
	}
	if len(pwd) < auth.MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", auth.MinPasswordLength)
	}

	code, err := promptValidatedLine(out, in, "Currency code (e.g. USD, EUR, NGN): ", validateCurrency)
	if err != nil {
		return err
	}
	curr, _ := models.LookupCurrency(code)
	tz, err := promptValidatedLine(out, in, "Timezone (IANA, e.g. America/New_York; press Enter for UTC): ", validateTimezone)
	if err != nil {
		return err
	}
	if tz == "" {
		tz = "UTC"
	}

	hash, err := auth.Hash(pwd)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if err := setupseed.SeedInitialOwner(ctx, db, q, setupseed.OwnerData{
		Email:          strings.ToLower(strings.TrimSpace(email)),
		PasswordHash:   hash,
		DisplayName:    strings.TrimSpace(name),
		CurrencyCode:   curr.Code,
		CurrencySymbol: curr.Symbol,
		Timezone:       tz,
		DashboardURL:   fmt.Sprintf("http://%s", cfg.Addr()),
	}); err != nil {
		if errors.Is(err, setupseed.ErrAlreadyInitialized) {
			return errors.New("Pennywise is already initialized. Use `./pennywise reset-password` if you've forgotten your login.")
		}
		return err
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "✅ Setup complete.")
	fmt.Fprintln(out, "Start the server: ./pennywise")
	fmt.Fprintf(out, "Then sign in at: http://%s/login\n", cfg.Addr())
	return nil
}

// promptValidatedLine prints label, reads a single line, validates it,
// and re-prompts on error up to 3 times before giving up. Returns the
// trimmed input on success.
func promptValidatedLine(out io.Writer, in *bufio.Reader, label string, validate func(string) error) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(out, label)
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if err := validate(line); err != nil {
			fmt.Fprintf(out, "  → %v\n", err)
			continue
		}
		return line, nil
	}
	return "", fmt.Errorf("validation failed after 3 attempts at %q", strings.TrimSpace(label))
}

// validateEmail accepts any RFC 5322 address. Trims whitespace.
func validateEmail(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("email is required")
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return errors.New("not a valid email address")
	}
	return nil
}

// validateNonEmpty rejects whitespace-only input.
func validateNonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("can't be empty")
	}
	return nil
}

// validateCurrency requires a code that resolves through models.LookupCurrency.
func validateCurrency(s string) error {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return errors.New("currency code is required")
	}
	if _, ok := models.LookupCurrency(s); !ok {
		return fmt.Errorf("unknown currency code %q (e.g. USD, EUR, NGN)", s)
	}
	return nil
}

// validateTimezone accepts an empty string (caller defaults to UTC) or
// any IANA name LoadLocation can resolve.
func validateTimezone(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if _, err := time.LoadLocation(s); err != nil {
		return fmt.Errorf("not a valid IANA timezone: %v", err)
	}
	return nil
}

// promptValidatedLine signature uses io.Writer so callers can route output
// to cmd.OutOrStderr() or any other sink.
var _ io.Writer = (*bufio.Writer)(nil)
