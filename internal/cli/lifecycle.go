package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arthurobo/pennywise/internal/config"
)

// startReadyTimeout is how long `pennywise start` polls /healthz to
// confirm the freshly-installed service actually came up before reporting
// success. launchd/systemd start fast on a working install (<1s).
const startReadyTimeout = 10 * time.Second

// newStartCommand returns `pennywise start`.
//
// `start` installs Pennywise as a real OS service (launchd LaunchAgent on
// macOS, systemd --user unit on Linux) the first time it's run, so the
// app auto-starts at every login + reboot. Subsequent runs are idempotent
// — they refresh the service file (so a `go install` upgrade is picked
// up) and ensure the process is running.
func newStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Install + run Pennywise as a service that survives reboot",
		Long: `Installs Pennywise as a user-scoped OS service on first run
and starts it. From then on, Pennywise auto-starts at every login and
restarts itself if it crashes. No further commands needed.

The service definition is written under your home directory so no sudo
is required:
  - macOS:  ~/Library/LaunchAgents/` + ServiceLabel + `.plist
  - Linux:  ~/.config/systemd/user/pennywise.service

Re-running ` + "`pennywise start`" + ` after upgrading the binary (e.g.
` + "`go install ...@latest`" + `) refreshes the service definition and
restarts the running process.`,
		RunE: runStart,
	}
}

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop Pennywise and remove it from auto-start",
		Long: `Stops the running Pennywise service and removes its OS service
definition. Pennywise will not run on next reboot.

To bring it back, run ` + "`pennywise start`" + `.`,
		RunE: runStop,
	}
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether Pennywise is installed and running",
		Long: `Reports the OS service state: installed (definition file present
+ registered with the supervisor), running (process alive), and PID +
dashboard URL when applicable.`,
		RunE: runStatus,
	}
}

func runStart(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	mgr, err := newServiceManager()
	if err != nil {
		return err
	}

	binPath, err := resolveBinaryPath()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}

	// Courtesy: clean up the stale PID file from the pre-service daemon
	// path. Older versions of Pennywise wrote it; current versions don't.
	cleanupLegacyPIDFile(cfg)

	if err := mgr.Install(cfg, binPath); err != nil {
		return err
	}

	// Wait for /healthz so the user knows the service actually came up
	// rather than crashed at startup (port in use, bad config, missing
	// migration, etc.). Failures surface here with the log path so they
	// can inspect immediately.
	url := fmt.Sprintf("http://%s/healthz", cfg.Addr())
	if err := waitForReady(url, startReadyTimeout); err != nil {
		return fmt.Errorf(
			"service installed but didn't respond on %s within %s — check %s",
			url, startReadyTimeout, cfg.LogPath())
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "✅ Pennywise is running (managed by %s)\n", mgr.PlatformName())
	fmt.Fprintf(out, "   Dashboard:    http://%s\n", cfg.Addr())
	fmt.Fprintf(out, "   Logs:         %s\n", cfg.LogPath())
	fmt.Fprintf(out, "   Service file: %s\n", mgr.ServiceFilePath())
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Pennywise will auto-start at every login. Run `pennywise stop` to remove it.")

	// Linux-only: warn about lingering when not enabled. The systemd-user
	// service still runs while the user is logged in, but won't survive
	// logout / unattended reboot until `loginctl enable-linger` is set.
	maybeWarnAboutLinger(cmd)
	return nil
}

func runStop(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	mgr, err := newServiceManager()
	if err != nil {
		return err
	}

	st, err := mgr.Status()
	if err != nil {
		return err
	}
	if !st.Installed && !st.Running {
		fmt.Fprintln(cmd.OutOrStdout(), "Pennywise isn't running.")
		// Still attempt cleanup in case a stale plist/unit somehow exists
		// without being registered.
		_ = mgr.Uninstall(cfg)
		cleanupLegacyPIDFile(cfg)
		return nil
	}

	if err := mgr.Uninstall(cfg); err != nil {
		return err
	}
	cleanupLegacyPIDFile(cfg)
	fmt.Fprintln(cmd.OutOrStdout(), "✅ Pennywise stopped and removed from auto-start.")
	fmt.Fprintln(cmd.OutOrStdout(), "   Run `pennywise start` to bring it back.")
	return nil
}

func runStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	mgr, err := newServiceManager()
	if err != nil {
		return err
	}
	st, err := mgr.Status()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	switch {
	case st.Running:
		fmt.Fprintln(out, "● Running")
		if st.PID > 0 {
			fmt.Fprintf(out, "   PID:          %d\n", st.PID)
		}
		fmt.Fprintf(out, "   Dashboard:    http://%s\n", cfg.Addr())
		fmt.Fprintf(out, "   Logs:         %s\n", cfg.LogPath())
		fmt.Fprintf(out, "   Service file: %s\n", mgr.ServiceFilePath())
	case st.Installed:
		fmt.Fprintln(out, "● Installed but not running")
		fmt.Fprintf(out, "   Service file: %s\n", mgr.ServiceFilePath())
		fmt.Fprintln(out, "   Run `pennywise start` to launch it.")
		os.Exit(3)
	default:
		fmt.Fprintln(out, "● Stopped (not installed)")
		fmt.Fprintln(out, "   Run `pennywise start` to install + launch.")
	}
	return nil
}

// resolveBinaryPath returns an absolute, symlink-resolved path to the
// running pennywise binary. The service file embeds this path, so we
// resolve symlinks (e.g. when `go install` drops a versioned binary that
// some package managers re-link) to capture the real target.
func resolveBinaryPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		return self, nil // fall back to the unresolved path
	}
	return resolved, nil
}

// cleanupLegacyPIDFile removes ~/.pennywise/pennywise.pid if present —
// it's a leftover from the pre-service daemon path that we no longer
// write but old installs may have on disk. Silent best-effort.
func cleanupLegacyPIDFile(cfg config.Config) {
	pidPath := cfg.PIDPath()
	if _, err := os.Stat(pidPath); err == nil {
		_ = os.Remove(pidPath)
	}
}

// waitForReady polls url every 200ms until it returns 2xx or timeout.
func waitForReady(url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		cancel()
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("readiness probe failed: %w", lastErr)
	}
	return errors.New("readiness probe timed out")
}

// maybeWarnAboutLinger nudges Linux users to enable lingering so the
// systemd --user service survives logout / unattended reboots. No-op on
// other platforms — the build-tagged stub returns false.
func maybeWarnAboutLinger(cmd *cobra.Command) {
	if runtime.GOOS != "linux" {
		return
	}
	enabled, err := LingerEnabledOrDefault()
	if err != nil || enabled {
		return
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "⚠  systemd --user services don't run after you log out unless you enable lingering:")
	fmt.Fprintln(out, "   sudo loginctl enable-linger $USER")
	fmt.Fprintln(out, "   (one-time; lets Pennywise survive logout + unattended reboots).")
}
