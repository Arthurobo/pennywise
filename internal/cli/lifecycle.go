package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arthurobo/pennywise/internal/config"
)

// Lifecycle file format:
//
//	<pid>\n<unix_start_seconds>\n
//
// Two lines: PID drives the alive/signal logic; start time is informational
// (status uses it for uptime display) and acts as a weak guard against PID
// reuse — if the running process at the recorded PID is older than the
// recorded start time, it isn't ours.
//
// Field separator is newline rather than JSON to keep the format readable
// with `cat` and to avoid pulling encoding/json into the lifecycle hot path.
const (
	pidFilePerms = 0o644

	// stopGraceTimeout is how long `pennywise stop` waits for the daemon to
	// shut down on SIGTERM before escalating to SIGKILL.
	stopGraceTimeout = 10 * time.Second

	// startReadyTimeout is how long `pennywise start` polls /healthz to
	// confirm the daemon actually came up before reporting success.
	startReadyTimeout = 5 * time.Second
)

// errNotRunning is returned by readPIDFile and readPIDFileAlive when no
// daemon is running for this data dir. Callers map it to the right
// surface-specific behavior.
var errNotRunning = errors.New("pennywise is not running")

// pidFileState captures the contents of a parsed PID file.
type pidFileState struct {
	PID       int
	StartTime time.Time
}

// newStartCommand returns the `pennywise start` subcommand.
func newStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start Pennywise as a background daemon",
		Long: `Forks the server into the background, detaches from this terminal,
and writes a PID file so you can stop or query it later. Logs are
appended to $PENNYWISE_DATA_DIR/pennywise.log.

For server installs, prefer systemd or launchd — they restart Pennywise
automatically on crash and at boot. The start/stop commands are aimed at
laptop / single-user deployments.`,
		RunE: runStart,
	}
}

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background Pennywise daemon",
		Long: `Sends SIGTERM to the running daemon and waits up to ` +
			stopGraceTimeout.String() + ` for graceful shutdown, then escalates
to SIGKILL if needed. Removes the PID file on success.`,
		RunE: runStop,
	}
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether Pennywise is running",
		Long: `Reports the daemon's PID, uptime, dashboard URL, and log file
location. Detects stale PID files (file present but process gone)
and exits with status 3 in that case so scripts can react.`,
		RunE: runStatus,
	}
}

func runStart(cmd *cobra.Command, _ []string) error {
	if err := requireUnix(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Already-running guard: if the file points at a live process, refuse
	// rather than silently leaving a second daemon contending for the port.
	if state, err := readPIDFileAlive(cfg.PIDPath()); err == nil {
		fmt.Fprintf(cmd.OutOrStderr(),
			"Pennywise is already running (PID %d). Use `./pennywise stop` first.\n",
			state.PID)
		return nil
	} else if !errors.Is(err, errNotRunning) && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check existing daemon: %w", err)
	}

	// Stale file from a previous run — clean it before forking.
	_ = os.Remove(cfg.PIDPath())

	logFile, err := os.OpenFile(cfg.LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", cfg.LogPath(), err)
	}
	defer func() { _ = logFile.Close() }()

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}
	// Resolve symlinks so re-exec works even when launched via ./pennywise.
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	child := exec.Command(self, "serve")
	child.Stdout = logFile
	child.Stderr = logFile
	child.Stdin = nil
	child.SysProcAttr = daemonSysProcAttr() // build-tagged: setsid on Unix
	// Pass through env so PENNYWISE_* and TZ etc. land in the child.
	child.Env = os.Environ()

	if err := child.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	if child.Process == nil {
		return errors.New("spawned daemon has no PID — refusing to write PID file")
	}

	state := pidFileState{
		PID:       child.Process.Pid,
		StartTime: time.Now().UTC(),
	}
	if err := writePIDFile(cfg.PIDPath(), state); err != nil {
		// Daemon is now running but we can't track it — warn loudly.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: daemon spawned (PID %d) but PID file write failed: %v\n",
			state.PID, err)
		return err
	}

	// Release the child so this process exits cleanly without becoming a
	// zombie keeper. cmd.Process is still readable for our PID grab above.
	_ = child.Process.Release()

	// Wait for /healthz before reporting success. If the child crashed on
	// startup (port in use, bad config), the user should see it now, not
	// the next time they try `status`.
	url := fmt.Sprintf("http://%s/healthz", cfg.Addr())
	if err := waitForReady(url, startReadyTimeout); err != nil {
		return fmt.Errorf(
			"daemon spawned (PID %d) but didn't respond on %s within %s — check %s",
			state.PID, url, startReadyTimeout, cfg.LogPath())
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✅ Pennywise started (PID %d)\n", state.PID)
	fmt.Fprintf(cmd.OutOrStdout(), "   Dashboard: http://%s\n", cfg.Addr())
	fmt.Fprintf(cmd.OutOrStdout(), "   Logs:      %s\n", cfg.LogPath())
	return nil
}

func runStop(cmd *cobra.Command, _ []string) error {
	if err := requireUnix(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	state, err := readPIDFile(cfg.PIDPath())
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(cmd.OutOrStdout(), "Pennywise isn't running.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}

	if !processAlive(state.PID) {
		fmt.Fprintln(cmd.OutOrStdout(), "Pennywise isn't running (stale PID file removed).")
		_ = os.Remove(cfg.PIDPath())
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Stopping Pennywise (PID %d)...\n", state.PID)
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", state.PID, err)
	}

	deadline := time.Now().Add(stopGraceTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(state.PID) {
			_ = os.Remove(cfg.PIDPath())
			fmt.Fprintln(cmd.OutOrStdout(), "✅ Stopped.")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Graceful shutdown timed out — escalate.
	fmt.Fprintln(cmd.ErrOrStderr(), "Daemon didn't exit on SIGTERM, sending SIGKILL...")
	if err := syscall.Kill(state.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("send SIGKILL to %d: %w", state.PID, err)
	}
	// Give it a brief moment, then clean up.
	time.Sleep(200 * time.Millisecond)
	_ = os.Remove(cfg.PIDPath())
	fmt.Fprintln(cmd.OutOrStdout(), "✅ Stopped (forced).")
	return nil
}

func runStatus(cmd *cobra.Command, _ []string) error {
	if err := requireUnix(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	state, err := readPIDFile(cfg.PIDPath())
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(cmd.OutOrStdout(), "● Stopped")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}

	if !processAlive(state.PID) {
		fmt.Fprintf(cmd.OutOrStdout(), "● Stopped (stale PID file at %s)\n", cfg.PIDPath())
		os.Exit(3)
		return nil // unreachable
	}

	uptime := time.Since(state.StartTime).Truncate(time.Second)
	fmt.Fprintln(cmd.OutOrStdout(), "● Running")
	fmt.Fprintf(cmd.OutOrStdout(), "   PID:       %d\n", state.PID)
	fmt.Fprintf(cmd.OutOrStdout(), "   Uptime:    %s\n", uptime)
	fmt.Fprintf(cmd.OutOrStdout(), "   Dashboard: http://%s\n", cfg.Addr())
	fmt.Fprintf(cmd.OutOrStdout(), "   Logs:      %s\n", cfg.LogPath())
	return nil
}

// --- helpers --------------------------------------------------------------

// readPIDFile parses the on-disk PID file. Returns os.ErrNotExist wrapped
// when the file is missing; format errors otherwise.
func readPIDFile(path string) (pidFileState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return pidFileState{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return pidFileState{}, fmt.Errorf("PID file %s is empty", path)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return pidFileState{}, fmt.Errorf("PID file %s: invalid PID: %w", path, err)
	}
	state := pidFileState{PID: pid, StartTime: time.Now().UTC()}
	if len(lines) >= 2 {
		if ts, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
			state.StartTime = time.Unix(ts, 0).UTC()
		}
	}
	return state, nil
}

// readPIDFileAlive reads the file AND verifies the recorded PID names a
// live process. Returns errNotRunning when the file is missing or stale.
func readPIDFileAlive(path string) (pidFileState, error) {
	state, err := readPIDFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pidFileState{}, errNotRunning
		}
		return pidFileState{}, err
	}
	if !processAlive(state.PID) {
		return pidFileState{}, errNotRunning
	}
	return state, nil
}

// writePIDFile atomically writes the given state. Atomicity comes from
// write-to-tmp + rename, so a concurrent reader never sees a partial file.
func writePIDFile(path string, state pidFileState) error {
	body := fmt.Sprintf("%d\n%d\n", state.PID, state.StartTime.Unix())
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), pidFilePerms); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// processAlive reports whether the given PID names a live process. Uses
// signal 0 — POSIX-defined as a permission/existence probe with no actual
// signal delivery.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means the process exists but we can't signal it (foreign user).
	// For the lifecycle commands' purpose that still counts as "alive".
	return errors.Is(err, syscall.EPERM)
}

// waitForReady polls url every 200ms until it returns 2xx or the timeout
// elapses. Returns nil on success.
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
	return fmt.Errorf("readiness probe timed out after %s", timeout)
}
