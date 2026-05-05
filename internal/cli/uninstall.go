package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Arthurobo/pennywise/internal/config"
)

// newUninstallCommand returns `pennywise uninstall`.
//
// Wipes Pennywise from this machine in three steps:
//   1. Stop + remove the OS service (LaunchAgent on macOS, systemd
//      --user unit on Linux). No-op on platforms without service support.
//   2. Delete the data directory ($PENNYWISE_DATA_DIR or ~/.pennywise) —
//      database, session secret, log file, anything Pennywise wrote.
//   3. Remove the installed binary at $GOBIN/pennywise (or
//      $GOPATH/bin/pennywise / $HOME/go/bin/pennywise).
//
// Asks for confirmation by default ("type 'yes' to confirm"). Pass
// --yes for scripted / unattended removal.
//
// Note on the binary: on Unix, deleting the currently-running binary is
// fine — the kernel keeps the in-memory image alive until the process
// exits. On Windows the OS locks the .exe, so we spawn a detached
// cmd.exe that waits a moment then deletes the file; the deletion
// completes silently a couple of seconds after this command exits.
func newUninstallCommand() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Permanently remove Pennywise from this machine",
		Long: `Stops and removes the OS service, deletes the data directory
(database, secret key, log file), and removes the installed binary at
$GOBIN/pennywise.

Asks for confirmation by default. Pass --yes for unattended removal.

What's NOT removed:
  - A development checkout / dev DB at ./dev/ inside a repo clone
    (those are a developer's concern; clear them with rm -rf dev/)
  - Your shell history, env-var settings in .bashrc/.zshrc, etc.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd, assumeYes)
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runUninstall(cmd *cobra.Command, assumeYes bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Discover paths up-front so the user sees exactly what's about to go
	// before they type 'yes'. No surprises.
	binPath, _ := updatedBinaryPath()
	dataDir := cfg.DataDir

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "This will permanently delete:")
	fmt.Fprintf(out, "  • OS service (auto-start on boot)\n")
	fmt.Fprintf(out, "  • Data directory: %s\n", dataDir)
	fmt.Fprintf(out, "    (includes pennywise.db, secret.key, pennywise.log)\n")
	if binPath != "" {
		fmt.Fprintf(out, "  • Installed binary: %s\n", binPath)
	}
	fmt.Fprintln(out, "")

	if !assumeYes {
		fmt.Fprint(out, "Type 'yes' to confirm: ")
		in := bufio.NewReader(cmd.InOrStdin())
		line, _ := in.ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(out, "Cancelled. Nothing was removed.")
			return nil
		}
	}

	errOut := cmd.ErrOrStderr()

	// Step 1 — service. Best-effort; we want to keep going even if the
	// service manager isn't available (e.g. unsupported OS).
	if mgr, err := newServiceManager(); err == nil {
		if err := mgr.Uninstall(cfg); err != nil {
			fmt.Fprintf(errOut, "warning: service teardown failed: %v\n", err)
		} else {
			fmt.Fprintln(out, "✓ Stopped service and removed auto-start")
		}
	} else {
		// Unsupported OS — no service to remove. Skip silently; not an
		// error condition, just a no-op.
	}

	// Step 2 — data directory. RemoveAll is idempotent (no error on
	// missing) but we still log it.
	if dataDir != "" {
		if err := os.RemoveAll(dataDir); err != nil {
			fmt.Fprintf(errOut, "warning: removing data dir %s: %v\n", dataDir, err)
		} else {
			fmt.Fprintf(out, "✓ Deleted %s\n", dataDir)
		}
	}

	// Step 3 — installed binary. The unix variant uses os.Remove
	// directly; the windows variant spawns a detached cmd.exe that
	// completes the delete a moment after this process exits (Windows
	// locks the running .exe and refuses direct deletion).
	if binPath != "" {
		err := removeRunningBinary(binPath)
		switch {
		case err == nil:
			fmt.Fprintf(out, "✓ Removed %s\n", binPath)
			if runtime.GOOS == "windows" {
				fmt.Fprintln(out, "  (deletion completes a moment after this command exits)")
			}
		case errors.Is(err, os.ErrNotExist):
			// Already gone — idempotent; nothing to log.
		default:
			fmt.Fprintf(errOut, "warning: removing %s: %v\n", binPath, err)
		}
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Pennywise has been uninstalled.")
	fmt.Fprintln(out, "To reinstall later: go install github.com/Arthurobo/pennywise/cmd/pennywise@latest")
	return nil
}
