//go:build !windows

package cli

import "syscall"

// daemonSysProcAttr returns the SysProcAttr that detaches a forked child
// from the parent's controlling terminal. Setsid makes the child a new
// session leader so it survives parent exit and isn't reaped on terminal
// close. Foreground=false keeps it from briefly grabbing the tty.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}

// requireUnix is a no-op on Unix; the Windows variant returns an error.
func requireUnix() error { return nil }
