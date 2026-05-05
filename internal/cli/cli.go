// Package cli implements the cobra command tree for the pennywise binary.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/config"
	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/server"
)

// Build-time identifiers — overridden via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// Execute runs the root command.
func Execute() error {
	root := &cobra.Command{
		Use:           "pennywise",
		Short:         "Pennywise — local-first personal expense tracker",
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runServe,
	}

	root.AddCommand(
		&cobra.Command{Use: "serve", Short: "Run the server (foreground)", RunE: runServe},
		newStartCommand(),
		newStopCommand(),
		newStatusCommand(),
		&cobra.Command{Use: "version", Short: "Print version info", Run: runVersion},
		newInitCommand(),
		newUpdateCommand(),
		newUninstallCommand(),
		&cobra.Command{Use: "reset-password", Short: "Reset the owner password", RunE: runResetPassword},
	)

	return root.Execute()
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return server.Run(cmd.Context(), cfg, server.VersionInfo{
		Version: version, Commit: commit, BuildDate: buildDate,
	})
}

func runVersion(_ *cobra.Command, _ []string) {
	fmt.Printf("pennywise %s (commit %s, built %s)\n", version, commit, buildDate)
}

func runResetPassword(cmd *cobra.Command, _ []string) error {
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
	owner, err := q.GetOwner(cmd.Context())
	if err != nil {
		return fmt.Errorf("no owner exists; run the server once and complete first-run setup: %w", err)
	}

	pw, err := promptPassword(fmt.Sprintf("New password for %s: ", owner.Email))
	if err != nil {
		return err
	}
	confirm, err := promptPassword("Confirm: ")
	if err != nil {
		return err
	}
	if pw != confirm {
		return errors.New("passwords do not match")
	}
	hash, err := auth.Hash(pw)
	if err != nil {
		return err
	}
	if err := q.UpdateOwnerPassword(cmd.Context(), sqlcgen.UpdateOwnerPasswordParams{
		PasswordHash: hash,
		UpdatedAt:    time.Now().UTC().Unix(),
	}); err != nil {
		return err
	}
	if err := q.DeleteAllSessions(cmd.Context()); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	fmt.Println("Password updated. All sessions revoked.")
	return nil
}


// promptPassword reads a password from the terminal without echoing.
// When stdin isn't a tty, falls back to reading a line from os.Stdin.
func promptPassword(label string) (string, error) {
	return promptPasswordFrom(bufio.NewReader(os.Stdin), label)
}

// promptPasswordFrom is the test-friendly variant: when stdin isn't a tty,
// reads from the provided buffered reader so tests can share the same
// bufio.Reader across line and password prompts (preventing the new-reader
// EOF problem from buffering the entire transcript on the first read).
// In production both paths converge on os.Stdin.
func promptPasswordFrom(fallback *bufio.Reader, label string) (string, error) {
	fmt.Print(label)
	if !term.IsTerminal(int(syscall.Stdin)) {
		line, err := fallback.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Allow the parent process to inject a context (e.g. for graceful shutdown).
func ExecuteWithContext(ctx context.Context) error {
	cmd := &cobra.Command{
		Use:          "pennywise",
		Short:        "Pennywise — local-first personal expense tracker",
		Version:      fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate),
		SilenceUsage: true,
		RunE:         runServe,
	}
	cmd.AddCommand(
		&cobra.Command{Use: "serve", Short: "Run the server (foreground)", RunE: runServe},
		newStartCommand(),
		newStopCommand(),
		newStatusCommand(),
		&cobra.Command{Use: "version", Short: "Print version info", Run: runVersion},
		newInitCommand(),
		newUpdateCommand(),
		newUninstallCommand(),
		&cobra.Command{Use: "reset-password", Short: "Reset the owner password", RunE: runResetPassword},
	)
	return cmd.ExecuteContext(ctx)
}
