//go:build !darwin && !linux && !windows

package cli

import "errors"

// LingerEnabledOrDefault is the no-op variant for platforms without a
// supported service manager. Always reports "enabled" so the linger-warn
// caller stays silent.
func LingerEnabledOrDefault() (bool, error) { return true, nil }

// newServiceManager returns a clear error on unsupported platforms.
// Pennywise's service install relies on POSIX-style supervisors
// (launchd / systemd --user). Windows users should run `pennywise serve`
// foreground under Task Scheduler or NSSM.
func newServiceManager() (ServiceManager, error) {
	return nil, errors.New("pennywise start/stop/status are unavailable on this OS — run `pennywise serve` foreground under your platform's service manager (Task Scheduler / NSSM on Windows)")
}
