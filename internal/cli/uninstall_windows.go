//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Windows process-creation flags. The Go syscall package on Windows
// exposes these via CreationFlags on SysProcAttr, but we redefine the
// numeric constants here to avoid importing golang.org/x/sys/windows
// just for two flags.
const (
	winDetachedProcess       uint32 = 0x00000008
	winCreateNewProcessGroup uint32 = 0x00000200
)

// removeRunningBinary handles the Windows case where the OS holds an
// exclusive lock on the currently-running .exe and refuses direct
// deletion.
//
// We spawn a detached `cmd.exe` background process that:
//   1. waits 2 seconds (giving the parent time to exit), then
//   2. deletes the .exe.
//
// Because the spawn is detached (DETACHED_PROCESS + new process group),
// the cmd.exe survives our exit. After we return and the OS releases
// the lock, the queued `del` succeeds.
//
// Returns os.ErrNotExist if the binary is already gone (idempotent
// uninstall). Otherwise returns nil on a successful spawn — the actual
// deletion completes asynchronously after our process exits.
func removeRunningBinary(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}

	arg := fmt.Sprintf(`timeout /t 2 /nobreak >nul & del /f /q "%s"`, path)
	cmd := exec.Command("cmd.exe", "/c", arg)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winDetachedProcess | winCreateNewProcessGroup,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("schedule windows self-delete: %w", err)
	}
	// Don't Wait() — we want it detached. The OS adopts it after we exit.
	return nil
}
