//go:build windows

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
	"unicode/utf16"

	"github.com/Arthurobo/pennywise/internal/config"
)

// taskName is the Task Scheduler task identifier. Same role as the
// LaunchAgent Label on macOS / the systemd unit name on Linux.
const taskName = "Pennywise"

// newServiceManager returns the Windows Task Scheduler implementation.
//
// The flow:
//   - Install writes a launcher .bat (env vars + redirect stdout/stderr
//     to the log file) into the data dir, then writes a Task Scheduler
//     task XML and registers it via `schtasks.exe /create /xml`.
//   - The task uses LogonTrigger (fires on user logon), InteractiveToken
//     + LeastPrivilege (runs as current user, no admin elevation), and
//     RestartOnFailure (3 retries, 1-minute interval).
//   - <Hidden>true</Hidden> suppresses the cmd window; the .bat runs
//     silently in the background.
//
// No admin / sudo equivalent is required because the task is scoped to
// the current user's context.
func newServiceManager() (ServiceManager, error) {
	return &windowsService{}, nil
}

type windowsService struct{}

func (s *windowsService) PlatformName() string    { return "task-scheduler" }
func (s *windowsService) ServiceFilePath() string { return `Task Scheduler\` + taskName }

func (s *windowsService) Install(cfg config.Config, binPath string) error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("locate current user: %w", err)
	}

	launcherPath := filepath.Join(cfg.DataDir, "pennywise-launcher.bat")
	if err := writeLauncherBat(launcherPath, binPath, cfg.LogPath(), pennywiseEnv()); err != nil {
		return fmt.Errorf("write launcher .bat: %w", err)
	}

	xmlContent, err := renderWindowsTaskXML(windowsTaskData{
		Username:     u.Username,
		LauncherPath: launcherPath,
		WorkingDir:   cfg.DataDir,
	})
	if err != nil {
		return fmt.Errorf("render task XML: %w", err)
	}

	// schtasks /xml is documented as accepting UTF-16 LE with BOM.
	// Modern Windows often accepts UTF-8 too, but UTF-16 is universally
	// supported across versions — pinning that for compatibility.
	tmpFile, err := os.CreateTemp("", "pennywise-task-*.xml")
	if err != nil {
		return fmt.Errorf("create tmp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmpFile.Write(encodeUTF16LE(xmlContent)); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write XML: %w", err)
	}
	_ = tmpFile.Close()

	// Stop any running instance from a prior install. Best-effort —
	// errors here are non-fatal; what matters is the next /create
	// succeeding.
	_, _ = exec.Command("schtasks.exe", "/end", "/tn", taskName).CombinedOutput()
	_, _ = exec.Command("schtasks.exe", "/delete", "/tn", taskName, "/f").CombinedOutput()
	_, _ = exec.Command("taskkill.exe", "/f", "/im", "pennywise.exe").CombinedOutput()

	// Create the task from XML. /f forces overwrite if for some reason
	// the previous /delete didn't take effect.
	out, err := exec.Command("schtasks.exe", "/create", "/tn", taskName, "/xml", tmpPath, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /create: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// Run it now so the user doesn't have to log out / log in to start
	// using Pennywise immediately.
	out, err = exec.Command("schtasks.exe", "/run", "/tn", taskName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *windowsService) Uninstall(cfg config.Config) error {
	// Stop the running task instance + kill any orphan pennywise.exe
	// that the task launched. taskkill is best-effort.
	_, _ = exec.Command("schtasks.exe", "/end", "/tn", taskName).CombinedOutput()
	_, _ = exec.Command("taskkill.exe", "/f", "/im", "pennywise.exe").CombinedOutput()

	out, err := exec.Command("schtasks.exe", "/delete", "/tn", taskName, "/f").CombinedOutput()
	if err != nil {
		body := strings.ToLower(string(out))
		if !strings.Contains(body, "does not exist") &&
			!strings.Contains(body, "cannot find") {
			return fmt.Errorf("schtasks /delete: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}

	// Remove the launcher .bat we wrote at install time.
	launcherPath := filepath.Join(cfg.DataDir, "pennywise-launcher.bat")
	if err := os.Remove(launcherPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Non-fatal; surface as warning to the caller via err return.
		return fmt.Errorf("remove launcher .bat: %w", err)
	}
	return nil
}

func (s *windowsService) Status() (ServiceStatus, error) {
	st := ServiceStatus{}

	// First: does the task exist? schtasks /query exits non-zero with
	// "ERROR: The system cannot find the file specified" when missing.
	if _, err := exec.Command("schtasks.exe", "/query", "/tn", taskName).CombinedOutput(); err != nil {
		return st, nil
	}
	st.Installed = true

	// Detailed query — verbose CSV gives us "Status" + "PID" columns.
	out, err := exec.Command("schtasks.exe", "/query", "/tn", taskName, "/v", "/fo", "CSV").CombinedOutput()
	if err != nil {
		return st, nil
	}
	st.Running, st.PID = parseTaskStatusCSV(string(out))
	return st, nil
}

// parseTaskStatusCSV pulls the Status (e.g. "Running" / "Ready") and PID
// columns out of `schtasks /query /v /fo CSV` output. The CSV has a
// header row of column names and one (or more) data rows; we read by
// column name to be resilient to Windows-version column reordering.
func parseTaskStatusCSV(body string) (running bool, pid int) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 2 {
		return false, 0
	}
	cols := splitCSV(lines[0])
	statusIdx, pidIdx := -1, -1
	for i, c := range cols {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "status":
			statusIdx = i
		case "pid":
			pidIdx = i
		}
	}
	for _, raw := range lines[1:] {
		row := splitCSV(raw)
		if statusIdx >= 0 && statusIdx < len(row) {
			if strings.EqualFold(strings.TrimSpace(row[statusIdx]), "Running") {
				running = true
			}
		}
		if pidIdx >= 0 && pidIdx < len(row) {
			if n, err := strconv.Atoi(strings.TrimSpace(row[pidIdx])); err == nil && n > 0 {
				pid = n
			}
		}
		if running {
			return
		}
	}
	return
}

// splitCSV is a minimal CSV row parser handling the only complication
// that schtasks /fo CSV emits: comma-separated, fields wrapped in
// double quotes. No embedded newlines, no escaped quotes.
func splitCSV(line string) []string {
	out := []string{}
	cur := strings.Builder{}
	inQuotes := false
	for _, r := range line {
		switch r {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if inQuotes {
				cur.WriteRune(r)
			} else {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	return out
}

// LingerEnabledOrDefault is the Windows no-op variant. Task Scheduler
// has no equivalent of systemd lingering — logon-trigger tasks fire at
// every user logon regardless. Always return true so the cross-platform
// linger-warn caller stays silent on Windows.
func LingerEnabledOrDefault() (bool, error) { return true, nil }

// --- launcher .bat -------------------------------------------------------

// writeLauncherBat writes the .bat that Task Scheduler invokes. The .bat
// sets every PENNYWISE_* env var (Task Scheduler's own env-var support
// is awkward; doing it here keeps things readable), then runs
// pennywise.exe with stdout+stderr appended to the log file.
//
// CRLF line endings are mandatory — cmd.exe rejects LF-only files.
func writeLauncherBat(path, binPath, logPath string, envVars map[string]string) error {
	var buf bytes.Buffer
	buf.WriteString("@echo off\r\n")
	for _, k := range sortedKeys(envVars) {
		// `set` lines don't tolerate quoted values with embedded quotes;
		// PENNYWISE_* values shouldn't contain quotes in practice, but
		// strip just in case.
		v := strings.ReplaceAll(envVars[k], "\"", "")
		fmt.Fprintf(&buf, "set %s=%s\r\n", k, v)
	}
	fmt.Fprintf(&buf, "\"%s\" serve >> \"%s\" 2>&1\r\n", binPath, logPath)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// --- task XML template ---------------------------------------------------

type windowsTaskData struct {
	Username     string
	LauncherPath string
	WorkingDir   string
}

// windowsTaskTemplate is the Task Scheduler task definition.
//
// Key choices:
//   - LogonTrigger fires at user logon.
//   - InteractiveToken + LeastPrivilege → runs as the current user with
//     standard rights, no admin elevation needed at install or run time.
//   - <Hidden>true</Hidden> suppresses the cmd window.
//   - RestartOnFailure: 3 attempts, 1-minute interval — same crash-recovery
//     posture as the macOS plist's KeepAlive=true and the systemd unit's
//     Restart=on-failure.
//   - ExecutionTimeLimit=PT0S → no time limit (long-running service).
const windowsTaskTemplate = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Pennywise expense tracker — auto-starts at user logon.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>{{.Username}}</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>{{.Username}}</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{{.LauncherPath}}</Command>
      <WorkingDirectory>{{.WorkingDir}}</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`

var windowsTaskTmpl = template.Must(template.New("task").Parse(windowsTaskTemplate))

func renderWindowsTaskXML(d windowsTaskData) (string, error) {
	var buf bytes.Buffer
	if err := windowsTaskTmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// encodeUTF16LE returns a UTF-16 little-endian byte slice prefixed with
// the BOM (FF FE). schtasks.exe /xml accepts this on every Windows
// version we support.
func encodeUTF16LE(s string) []byte {
	out := []byte{0xFF, 0xFE}
	codepoints := utf16.Encode([]rune(s))
	for _, c := range codepoints {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}
