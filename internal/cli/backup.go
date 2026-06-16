package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arthurobo/pennywise/internal/config"
	pwdb "github.com/Arthurobo/pennywise/internal/db"
)

func newBackupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Export the database and secret key to a .zip archive",
		Long: `Creates a consistent snapshot of the Pennywise database and the
session secret key, bundled into a single .zip archive.

You will be prompted for a destination path. Press Enter to accept the
default location (your Desktop folder).

The archive contains:
  penalty.db  — full database snapshot (VACUUM INTO)
  secret.key  — session secret for decrypting API keys / bot tokens

Restore with: penguin restore <archive.zip>`,
		RunE: runBackup,
	}
}

func newRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <backup.zip>",
		Short: "Restore from a .zip backup archive",
		Long: `Restores the Pennywise database and session secret from a .zip
backup created by 'pennywise backup'.

The server is stopped automatically (if running). The current database is
saved to a safety copy before replacement. Run 'pennywise start' afterwards
to bring the server back online.`,
		Args: cobra.ExactArgs(1),
		RunE: runRestore,
	}
}

func runBackup(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	defaultPath := backupDefaultPath(cfg)
	fmt.Fprintf(cmd.OutOrStdout(), "Export to [%s]: ", defaultPath)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	dst := strings.TrimSpace(line)
	if dst == "" {
		dst = defaultPath
	}
	if !strings.HasSuffix(strings.ToLower(dst), ".zip") {
		dst += ".zip"
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Creating backup ...\n")
	if err := pwdb.Archive(cfg.DBPath(), cfg.SecretPath(), dst); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✅ Backup saved to %s\n", dst)
	fmt.Fprintf(cmd.OutOrStdout(), "   Contains: pennywise.db + secret.key\n")
	return nil
}

func runRestore(cmd *cobra.Command, args []string) error {
	src := args[0]

	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("backup not found at %s", src)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Stop the service if it's running.
	stopped, stopErr := stopService(cfg)
	if stopErr != nil {
		return fmt.Errorf("stop service: %w", stopErr)
	}
	if stopped {
		fmt.Fprintf(cmd.OutOrStdout(), "✅ Service stopped.\n")
	}

	if _, mgrErr := newServiceManager(); mgrErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "⚠  Service management isn't available on this platform.\n")
		fmt.Fprintf(cmd.ErrOrStderr(), "   Make sure the server isn't running before proceeding.\n")
	}

	// Keep a safety copy of the current database.
	dbPath := cfg.DBPath()
	safetyPath := dbPath + ".pre-restore-" + time.Now().UTC().Format("20060102-150405")
	if _, statErr := os.Stat(dbPath); statErr == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Saving current database to %s ...\n", safetyPath)
		if err := copyFileBackup(dbPath, safetyPath); err != nil {
			return fmt.Errorf("create safety copy: %w", err)
		}
	}

	// Remove WAL and SHM sidecar files so SQLite starts fresh.
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + ext)
	}

	// Keep a safety copy of the current secret key if present.
	secretPath := cfg.SecretPath()
	if _, statErr := os.Stat(secretPath); statErr == nil {
		secretSafety := secretPath + ".pre-restore-" + time.Now().UTC().Format("20060102-150405")
		if err := copyFileBackup(secretPath, secretSafety); err != nil {
			return fmt.Errorf("create safety copy of secret key: %w", err)
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Extracting backup ...\n")
	extracted, err := pwdb.ExtractArchive(src, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✅ Database restored from %s\n", src)
	for _, p := range extracted {
		fmt.Fprintf(cmd.OutOrStdout(), "   %s\n", p)
	}
	if safetyPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "   Previous database saved to %s\n", safetyPath)
	}

	hasSecret := false
	for _, p := range extracted {
		if strings.HasSuffix(p, "secret.key") {
			hasSecret = true
			break
		}
	}
	if !hasSecret {
		fmt.Fprintf(cmd.OutOrStdout(), "⚠  Backup does not contain secret.key.\n")
		fmt.Fprintf(cmd.OutOrStdout(), "   If you use PENNYWISE_SESSION_SECRET env var, set it and you're fine.\n")
		fmt.Fprintf(cmd.OutOrStdout(), "   Otherwise, Telegram / LLM will need re-setup.\n")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "   Run 'pennywise start' to start the server.\n")
	return nil
}

func backupDefaultPath(cfg config.Config) string {
	base := "pennywise-backup-" + time.Now().UTC().Format("2006-01-02") + ".zip"
	desktop := desktopPath()
	if desktop != "" {
		if info, err := os.Stat(desktop); err == nil && info.IsDir() {
			return filepath.Join(desktop, base)
		}
	}
	return base
}

func desktopPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Desktop")
}

func copyFileBackup(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Sync()
}

// stopService stops the service cleanly if a platform service manager is
// available. When the platform doesn't support service management (Windows,
// or unsupported), the caller is warned that manual intervention is needed.
func stopService(cfg config.Config) (stopped bool, _ error) {
	mgr, err := newServiceManager()
	if err != nil {
		return false, nil // not a fatal error — caller warns the user
	}
	st, err := mgr.Status()
	if err != nil {
		return false, nil
	}
	if !st.Running && !st.Installed {
		return false, nil
	}
	if err := mgr.Uninstall(cfg); err != nil {
		return st.Running, err
	}
	cleanupLegacyPIDFile(cfg)
	return true, nil
}
