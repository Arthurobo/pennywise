//go:build windows

package cli

import (
	"errors"
	"syscall"
)

// daemonSysProcAttr is a stub on Windows. start/stop/status return an
// error before this is reached, so production never calls it; the file
// exists to keep the build passing on windows/amd64 cross-compiles.
func daemonSysProcAttr() *syscall.SysProcAttr { return nil }

// requireUnix returns a clear error on Windows. The start/stop/status
// commands rely on POSIX signals (SIGTERM/SIGKILL) and setsid; mapping
// them to Windows job objects + Ctrl+C events is a separate effort.
//
// Windows users should run Pennywise foreground via Task Scheduler, NSSM,
// or `pennywise serve` from a Windows Service shim.
func requireUnix() error {
	return errors.New("start/stop/status are Unix-only — on Windows, run with `pennywise serve` under Task Scheduler or NSSM")
}
