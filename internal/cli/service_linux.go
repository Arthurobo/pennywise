//go:build linux

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/Arthurobo/pennywise/internal/config"
)

// systemdUnitName is the file name on disk and the unit identifier passed
// to systemctl. We include the .service suffix in commands but the file
// on disk doesn't need it for systemctl --user; both forms work.
const systemdUnitName = "pennywise.service"

func newServiceManager() (ServiceManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home dir: %w", err)
	}
	return &linuxService{
		home:     home,
		unitPath: filepath.Join(home, ".config/systemd/user", systemdUnitName),
	}, nil
}

type linuxService struct {
	home     string
	unitPath string
}

func (s *linuxService) PlatformName() string    { return "systemd-user" }
func (s *linuxService) ServiceFilePath() string { return s.unitPath }

// Install writes the systemd unit, reloads the user daemon, and enables
// the unit so it starts now and on every login. After a successful
// install we check `loginctl show-user` for Linger=yes and warn the user
// if it isn't set — without lingering, systemd --user only runs while the
// user is interactively logged in, which usually isn't what someone
// wanting "auto-restart on boot" expects.
func (s *linuxService) Install(cfg config.Config, binPath string) error {
	unit, err := renderLinuxUnit(linuxUnitData{
		BinaryPath: binPath,
		EnvVars:    pennywiseEnv(),
	})
	if err != nil {
		return fmt.Errorf("render unit file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.unitPath), 0o755); err != nil {
		return fmt.Errorf("create user-units dir: %w", err)
	}
	if err := os.WriteFile(s.unitPath, unit, 0o644); err != nil {
		return fmt.Errorf("write unit file %s: %w", s.unitPath, err)
	}

	if out, err := runSystemctlUser("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w (%s)", err, strings.TrimSpace(out))
	}
	if out, err := runSystemctlUser("enable", "--now", systemdUnitName); err != nil {
		return fmt.Errorf("enable --now: %w (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

// Uninstall stops + disables + removes. Errors from systemctl are
// swallowed when the unit is already absent (idempotency).
func (s *linuxService) Uninstall(_ config.Config) error {
	_, _ = runSystemctlUser("disable", "--now", systemdUnitName)
	if err := os.Remove(s.unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove unit file: %w", err)
	}
	_, _ = runSystemctlUser("daemon-reload")
	return nil
}

// Status reports installed (unit file present) and running (is-active).
// Reads MainPID via systemctl show.
func (s *linuxService) Status() (ServiceStatus, error) {
	st := ServiceStatus{}
	if _, err := os.Stat(s.unitPath); err == nil {
		st.Installed = true
	}
	if out, err := runSystemctlUser("is-active", systemdUnitName); err == nil &&
		strings.TrimSpace(out) == "active" {
		st.Running = true
	}
	if st.Running {
		if out, err := runSystemctlUser("show", "--property=MainPID", "--value", systemdUnitName); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(out)); perr == nil && pid > 0 {
				st.PID = pid
			}
		}
	}
	return st, nil
}

// LingerEnabledOrDefault reports whether the current user has linger
// enabled (i.e. `loginctl enable-linger <user>`). Without lingering,
// systemd --user services only run while the user is interactively
// logged in — auto-restart-on-boot expectations break.
//
// Returns false on any uncertainty (loginctl missing, permission error)
// so the caller errs on the side of nudging the user.
func LingerEnabledOrDefault() (bool, error) {
	u, err := user.Current()
	if err != nil {
		return false, err
	}
	out, err := exec.Command("loginctl", "show-user", u.Username, "--property=Linger", "--value").Output()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "yes", nil
}

// runSystemctlUser is the single chokepoint for `systemctl --user ...`.
// Captures combined output for error context.
func runSystemctlUser(args ...string) (string, error) {
	full := append([]string{"--user"}, args...)
	out, err := exec.Command("systemctl", full...).CombinedOutput()
	return string(out), err
}

// --- unit-file template -------------------------------------------------

type linuxUnitData struct {
	BinaryPath string
	EnvVars    map[string]string
}

// SortedEnv returns env vars in deterministic order. Same rationale as
// the macOS plist renderer.
func (d linuxUnitData) SortedEnv() []struct{ K, V string } {
	keys := sortedKeys(d.EnvVars)
	out := make([]struct{ K, V string }, len(keys))
	for i, k := range keys {
		out[i] = struct{ K, V string }{K: k, V: d.EnvVars[k]}
	}
	return out
}

const linuxUnitTemplate = `[Unit]
Description=Pennywise expense tracker
After=network.target

[Service]
Type=simple
ExecStart={{ .BinaryPath }} serve
Restart=on-failure
RestartSec=5s
{{- range .SortedEnv }}
Environment={{ .K }}={{ .V }}
{{- end }}

[Install]
WantedBy=default.target
`

var linuxUnitTmpl = template.Must(template.New("unit").Parse(linuxUnitTemplate))

func renderLinuxUnit(d linuxUnitData) ([]byte, error) {
	var buf bytes.Buffer
	if err := linuxUnitTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
