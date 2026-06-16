//go:build darwin

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Arthurobo/pennywise/internal/config"
)

// LingerEnabledOrDefault is the macOS no-op variant — there's no
// equivalent of systemd's user-lingering on macOS (LaunchAgents always
// run at user login regardless), so we return true to keep the cross-
// platform `maybeWarnAboutLinger` caller silent here.
func LingerEnabledOrDefault() (bool, error) { return true, nil }

// newServiceManager returns the macOS launchd implementation.
func newServiceManager() (ServiceManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home dir: %w", err)
	}
	return &darwinService{
		home:      home,
		plistPath: filepath.Join(home, "Library/LaunchAgents", ServiceLabel+".plist"),
	}, nil
}

type darwinService struct {
	home      string
	plistPath string
}

func (s *darwinService) PlatformName() string    { return "launchd" }
func (s *darwinService) ServiceFilePath() string { return s.plistPath }

// Install writes the LaunchAgent plist and registers it. Subsequent calls
// refresh the plist (so a `go install` upgrade is picked up) and
// kickstart the existing service.
func (s *darwinService) Install(cfg config.Config, binPath string) error {
	plist, err := renderDarwinPlist(darwinPlistData{
		Label:      ServiceLabel,
		BinaryPath: binPath,
		HomeDir:    s.home,
		LogPath:    cfg.LogPath(),
		EnvVars:    pennywiseEnv(),
	})
	if err != nil {
		return fmt.Errorf("render plist: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(s.plistPath, plist, 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", s.plistPath, err)
	}

	target := s.guiTarget()
	// Bootstrap. If already loaded, bootstrap fails — bootout the
	// stale service first so the next bootstrap re-reads the plist on
	// disk. Plain `kickstart -k` would only restart the in-memory
	// plist, which still has the old binary path / env vars — wrong
	// for the upgrade path.
	if out, err := runLaunchctl("bootstrap", target, s.plistPath); err != nil {
		if !isAlreadyLoaded(out) {
			return fmt.Errorf("launchctl bootstrap: %w (%s)", err, strings.TrimSpace(out))
		}
		// Tear down the stale service, then re-bootstrap with a short
		// backoff. launchd can report the service as "already loaded"
		// for a beat after bootout — retry a few times so `update` and
		// `start` don't flake on timing.
		_, _ = runLaunchctl("bootout", s.unitTarget())
		var lastErr error
		for range 5 {
			time.Sleep(200 * time.Millisecond)
			if out2, err2 := runLaunchctl("bootstrap", target, s.plistPath); err2 == nil {
				return nil
			} else {
				lastErr = fmt.Errorf("re-bootstrap after bootout: %w (%s)", err2, strings.TrimSpace(out2))
			}
		}
		return lastErr
	}
	return nil
}

// Uninstall boots out the service and removes the plist. Idempotent —
// missing service / file is treated as success.
func (s *darwinService) Uninstall(_ config.Config) error {
	// bootout is fine even if not loaded; ignore those errors.
	if _, err := runLaunchctl("bootout", s.unitTarget()); err != nil {
		// Swallow "no such service" / "not currently loaded".
		// Anything else is a real failure but we still try to remove
		// the plist so a subsequent install starts from a clean slate.
	}
	if err := os.Remove(s.plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// Status reports installed/running by combining the on-disk plist
// presence with `launchctl list <label>`. The list command exits 113 on
// "not found" — we treat that as not-running but still installed if the
// plist file is on disk (the user may have stopped it manually).
func (s *darwinService) Status() (ServiceStatus, error) {
	st := ServiceStatus{}
	if _, err := os.Stat(s.plistPath); err == nil {
		st.Installed = true
	}
	out, err := runLaunchctl("list", ServiceLabel)
	if err != nil {
		// Exit 113 → service unknown to launchd. Not running.
		return st, nil
	}
	st.PID = parseLaunchctlListPID(out)
	if st.PID > 0 {
		st.Running = true
	}
	return st, nil
}

// guiTarget is the bootstrap "domain": gui/<uid>. User-scoped LaunchAgents
// land here so they run when the user logs in.
func (s *darwinService) guiTarget() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// unitTarget is gui/<uid>/<label>, the addressable identifier of an
// already-loaded service.
func (s *darwinService) unitTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), ServiceLabel)
}

// runLaunchctl runs `launchctl <args...>` and returns combined output +
// error. Trims to keep error messages readable.
func runLaunchctl(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// isAlreadyLoaded heuristically detects the "service already bootstrapped"
// failure mode. launchctl's exit code is generic; we have to grep its
// stderr output.
func isAlreadyLoaded(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "already loaded") ||
		strings.Contains(low, "service already loaded") ||
		strings.Contains(low, "service is already loaded") ||
		strings.Contains(low, "bootstrap failed") && strings.Contains(low, "5: input/output error")
}

// parseLaunchctlListPID picks the "PID" field out of `launchctl list
// <label>` output. The format is a plist-ish key/value block:
//
//	{
//	    "PID" = 1234;
//	    "LimitLoadToSessionType" = "Aqua";
//	    ...
//	}
//
// Returns 0 if not running (the field reads "-" or is absent).
func parseLaunchctlListPID(out string) int {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\"PID\"") {
			continue
		}
		// Format: "PID" = 1234;
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		val = strings.TrimSuffix(val, ";")
		val = strings.TrimSpace(val)
		if val == "-" || val == "" {
			return 0
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// --- plist template -----------------------------------------------------

type darwinPlistData struct {
	Label      string
	BinaryPath string
	HomeDir    string
	LogPath    string
	EnvVars    map[string]string
}

// SortedEnv returns env vars in deterministic order so tests can pin
// exact rendered content and so two installs against the same env produce
// byte-identical plists (no spurious mtime churn).
func (d darwinPlistData) SortedEnv() []struct{ K, V string } {
	keys := sortedKeys(d.EnvVars)
	out := make([]struct{ K, V string }, len(keys))
	for i, k := range keys {
		out[i] = struct{ K, V string }{K: k, V: d.EnvVars[k]}
	}
	return out
}

const darwinPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{ .Label }}</string>

  <key>ProgramArguments</key>
  <array>
    <string>{{ .BinaryPath }}</string>
    <string>serve</string>
  </array>
{{- with .SortedEnv }}

  <key>EnvironmentVariables</key>
  <dict>
{{- range . }}
    <key>{{ .K }}</key>
    <string>{{ .V }}</string>
{{- end }}
  </dict>
{{- end }}

  <key>WorkingDirectory</key>
  <string>{{ .HomeDir }}</string>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>StandardOutPath</key>
  <string>{{ .LogPath }}</string>
  <key>StandardErrorPath</key>
  <string>{{ .LogPath }}</string>
</dict>
</plist>
`

var darwinPlistTmpl = template.Must(template.New("plist").Parse(darwinPlistTemplate))

func renderDarwinPlist(d darwinPlistData) ([]byte, error) {
	var buf bytes.Buffer
	if err := darwinPlistTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
