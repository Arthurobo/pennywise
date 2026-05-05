package cli

import (
	"os"
	"sort"
	"strings"

	"github.com/Arthurobo/pennywise/internal/config"
)

// ServiceLabel is the canonical name across platforms — the launchd Label
// and the systemd unit name (sans .service). Reverse-DNS to avoid clashes
// with anything else the user might have installed.
const ServiceLabel = "com.pennywise.app"

// ServiceManager hides the platform-specific bits behind one interface.
// macOS implements it via launchd LaunchAgents; Linux via systemd --user
// units; Windows returns a clear "use Task Scheduler" error from
// newServiceManager.
//
// The contract is deliberately small. Install/Uninstall are idempotent —
// running them twice is a no-op (or a refresh, in Install's case). Status
// distinguishes installed-but-stopped from never-installed so the CLI
// surface can give a precise message.
type ServiceManager interface {
	// Install writes the service definition to disk, registers it with
	// the OS supervisor, and ensures it's running. Re-running Install
	// after an upgrade refreshes the service file and kickstarts the
	// process — so `go install` followed by `pennywise start` picks up
	// the new binary path automatically.
	Install(cfg config.Config, binPath string) error

	// Uninstall stops the running process, removes it from the OS
	// supervisor, and deletes the service definition file. Idempotent —
	// running on an already-uninstalled system is a no-op.
	Uninstall(cfg config.Config) error

	// Status reports whether the service is installed (definition file
	// present + registered) and whether the process is currently running.
	Status() (ServiceStatus, error)

	// PlatformName is "launchd", "systemd-user", etc. — used in user
	// messages so they know which supervisor manages Pennywise on this OS.
	PlatformName() string

	// ServiceFilePath returns the absolute path to the on-disk service
	// definition (plist or .service file). Useful for status output and
	// for users who want to inspect or hand-edit it.
	ServiceFilePath() string
}

// ServiceStatus is the platform-agnostic status payload.
type ServiceStatus struct {
	Installed bool  // service definition file exists + registered
	Running   bool  // process currently alive
	PID       int   // 0 if not running or unknown
}

// pennywiseEnv collects the PENNYWISE_* environment variables present in
// the parent process. We bake these into the service file at install time
// because launchd / systemd don't read shell profiles, so the user's
// `export PENNYWISE_PORT=...` would otherwise vanish at boot.
//
// Returns a map sorted by key (sortedKeys helper) so generated service
// files are deterministic across runs — important for tests asserting
// rendered content.
func pennywiseEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		k := kv[:eq]
		if !strings.HasPrefix(k, "PENNYWISE_") {
			continue
		}
		out[k] = kv[eq+1:]
	}
	return out
}

// sortedKeys returns the map keys in lexicographic order. Used by the
// service-file renderers so plists / unit files emit env vars in a stable
// order — tests can pin exact strings.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
