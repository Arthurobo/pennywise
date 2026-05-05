//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Windows process-creation flags. The Go syscall package on Windows
// exposes these via CreationFlags on SysProcAttr; we redefine the
// numeric constants here to avoid pulling in golang.org/x/sys/windows
// for two values.
const (
	winDetachedProcess       uint32 = 0x00000008
	winCreateNewProcessGroup uint32 = 0x00000200
)

// removeRunningBinary handles Windows's "can't delete a running .exe"
// constraint via the standard rename trick:
//
//  1. Rename pennywise.exe → pennywise.exe.old. Windows allows
//     MoveFile on a running executable; only DeleteFile is blocked.
//     After this, the original path is gone — `pennywise --version`
//     etc. fail with "command not found" immediately, which is what
//     the user expects after uninstall.
//  2. Spawn a detached cmd.exe that deletes the .old file a few
//     seconds later (after our process exits and any AV scan
//     releases its handle), with a retry loop in case Defender holds
//     the lock briefly.
//
// Even if step 2 fails entirely (group policy disabling cmd, weird
// AV behavior), the user-visible outcome is "uninstalled" because
// step 1 always succeeds. Worst case: a leftover .old file in the
// bin dir, never the original .exe.
//
// Returns os.ErrNotExist if the binary is already gone (idempotent
// uninstall). Otherwise returns nil after a successful rename; the
// .old cleanup is best-effort and asynchronous.
func removeRunningBinary(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}

	oldPath := path + ".old"
	// Clean up any leftover .old from a previous interrupted uninstall
	// so the rename below has a free target.
	_ = os.Remove(oldPath)

	if err := os.Rename(path, oldPath); err != nil {
		return fmt.Errorf("rename for delayed delete: %w", err)
	}

	// Best-effort: schedule deletion of the renamed file. If this
	// fails, the user still sees the uninstall as successful — only
	// side effect is the .old file lingering on disk.
	_ = scheduleWindowsDelete(oldPath)
	return nil
}

// scheduleWindowsDelete spawns a detached cmd.exe that waits a few
// seconds (for AV / handles to release), then retries `del` up to 10
// times with 1-second gaps. Uses a tmp .bat for the script to dodge
// cmd.exe's `/c` quote-stripping when paths contain spaces.
func scheduleWindowsDelete(path string) error {
	batBody := "@echo off\r\n" +
		"timeout /t 3 /nobreak >nul\r\n" +
		"set /a count=0\r\n" +
		":retry\r\n" +
		"del /f /q \"" + path + "\" 2>nul\r\n" +
		"if not exist \"" + path + "\" goto done\r\n" +
		"if %count% geq 10 goto done\r\n" +
		"set /a count+=1\r\n" +
		"timeout /t 1 /nobreak >nul\r\n" +
		"goto retry\r\n" +
		":done\r\n" +
		"del /f /q \"%~f0\"\r\n"

	batPath := filepath.Join(os.TempDir(), "pennywise-self-delete.bat")
	if err := os.WriteFile(batPath, []byte(batBody), 0o644); err != nil {
		return fmt.Errorf("write self-delete .bat: %w", err)
	}

	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	cmd := exec.Command(comspec, "/c", batPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winDetachedProcess | winCreateNewProcessGroup,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("schedule windows self-delete: %w", err)
	}
	return nil
}
