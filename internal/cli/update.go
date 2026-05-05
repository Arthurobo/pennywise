package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/Arthurobo/pennywise/internal/config"
)

// modulePath is the canonical Go module path for `go install`. Pinned
// here (not derived from runtime/debug.ReadBuildInfo) so updates work
// even when the binary was built with custom -ldflags or from a fork
// during development.
const modulePath = "github.com/Arthurobo/pennywise/cmd/pennywise"

// newUpdateCommand returns `pennywise update`.
//
// The flow:
//  1. Find `go` on PATH. Fail clearly if not found.
//  2. Run `go install <module>@latest`. Output streams live so the user
//     sees the download progress.
//  3. If a Pennywise service is currently installed (managed by launchd
//     or systemd --user), re-run Install with the freshly-installed
//     binary path. That regenerates the service file and restarts the
//     process so it's running the new binary.
//  4. /healthz readiness probe before reporting success.
//
// If no service is installed (foreground use, or never started), we
// just bail after `go install` succeeds and tell the user how to launch.
func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Pull the latest Pennywise via go install + restart the service",
		Long: `Runs ` + "`go install " + modulePath + "@latest`" + ` to fetch the latest
release, then restarts the running Pennywise service so it's actually
using the new binary. Requires Go to be installed.

If no service is running, ` + "`update`" + ` just installs the new binary and
prints how to start it.`,
		RunE: runUpdate,
	}
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	goBin, err := exec.LookPath("go")
	if err != nil {
		return errors.New("`go` isn't on PATH. Install Go from https://go.dev/dl/ then re-run `pennywise update`")
	}

	fmt.Fprintln(out, "↓ Pulling latest Pennywise...")
	install := exec.Command(goBin, "install", modulePath+"@latest")
	install.Stdout = out
	install.Stderr = errOut
	install.Env = os.Environ()
	if err := install.Run(); err != nil {
		return fmt.Errorf("go install failed: %w", err)
	}

	updatedBin, err := updatedBinaryPath()
	if err != nil {
		return fmt.Errorf("locate updated binary: %w", err)
	}
	if _, err := os.Stat(updatedBin); err != nil {
		return fmt.Errorf("updated binary not at %s: %w", updatedBin, err)
	}
	fmt.Fprintf(out, "  → installed: %s\n", updatedBin)

	mgr, err := newServiceManager()
	if err != nil {
		// Unsupported OS — succeed and tell the user to launch manually.
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "✅ Pennywise updated. Launch with `pennywise serve`.")
		return nil
	}

	st, statusErr := mgr.Status()
	if statusErr != nil {
		// Status check failed — surface it but don't abort. The new binary
		// is already on disk; user can run `pennywise start` manually.
		fmt.Fprintf(errOut, "  (couldn't read service status: %v)\n", statusErr)
	}
	if !st.Installed {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "✅ Pennywise updated. Run `pennywise start` to launch it.")
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "↻ Refreshing service to use the new binary...")
	if err := mgr.Install(cfg, updatedBin); err != nil {
		return fmt.Errorf("refresh service: %w", err)
	}

	url := fmt.Sprintf("http://%s/healthz", cfg.Addr())
	if err := waitForReady(url, startReadyTimeout); err != nil {
		return fmt.Errorf(
			"service refreshed but didn't respond on %s within %s — check %s",
			url, startReadyTimeout, cfg.LogPath())
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "✅ Pennywise updated and restarted.")
	return nil
}

// updatedBinaryPath returns the path `go install` writes to. Honors GOBIN
// when set, falls back to `$HOME/go/bin/pennywise`. We resolve this
// independently rather than calling `os.Executable()` because the
// currently-running process is the OLD binary; we want the location of
// the freshly-installed one.
//
// On Windows the file would be pennywise.exe — but the lifecycle commands
// already refuse to run on Windows, and `pennywise update` is most useful
// alongside them.
func updatedBinaryPath() (string, error) {
	binaryName := "pennywise"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		return filepath.Join(gobin, binaryName), nil
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "bin", binaryName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "go", "bin", binaryName), nil
}
