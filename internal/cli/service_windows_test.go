//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderWindowsTaskXML_ContainsRequiredElements(t *testing.T) {
	out, err := renderWindowsTaskXML(windowsTaskData{
		Username:     "DESKTOP-X\\arthur",
		LauncherPath: `C:\Users\arthur\.pennywise\pennywise-launcher.bat`,
		WorkingDir:   `C:\Users\arthur\.pennywise`,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-16"?>`,
		`<LogonTrigger>`,
		`<UserId>DESKTOP-X\arthur</UserId>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<RunLevel>LeastPrivilege</RunLevel>`,
		`<Hidden>true</Hidden>`,
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`<RestartOnFailure>`,
		`<Command>C:\Users\arthur\.pennywise\pennywise-launcher.bat</Command>`,
		`<WorkingDirectory>C:\Users\arthur\.pennywise</WorkingDirectory>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("XML missing %q\n--- full XML ---\n%s", want, out)
		}
	}
}

func TestEncodeUTF16LE_HasBOMAndCorrectBytes(t *testing.T) {
	got := encodeUTF16LE("AB")
	want := []byte{0xFF, 0xFE, 'A', 0x00, 'B', 0x00}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d (got=%v)", len(got), len(want), got)
	}
	for i, b := range want {
		if got[i] != b {
			t.Fatalf("byte %d: got %#x want %#x", i, got[i], b)
		}
	}
}

func TestWriteLauncherBat_HasCRLFAndEnvVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launcher.bat")
	envVars := map[string]string{
		"PENNYWISE_PORT":     "9002",
		"PENNYWISE_DATA_DIR": `C:\Users\arthur\.pennywise`,
	}
	if err := writeLauncherBat(path, `C:\go\bin\pennywise.exe`, `C:\log\pennywise.log`, envVars); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"@echo off\r\n",
		"set PENNYWISE_DATA_DIR=C:\\Users\\arthur\\.pennywise\r\n",
		"set PENNYWISE_PORT=9002\r\n",
		`"C:\go\bin\pennywise.exe" serve >> "C:\log\pennywise.log" 2>&1` + "\r\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf(".bat missing %q\n--- full content ---\n%s", want, s)
		}
	}
	// Reject LF-only line endings (cmd.exe rejects them).
	if strings.Contains(s, "\n") && !strings.Contains(s, "\r\n") {
		t.Fatalf(".bat has LF-only line endings — cmd.exe will reject")
	}
}

func TestParseTaskStatusCSV_Running(t *testing.T) {
	body := `"HostName","TaskName","Next Run Time","Status","Logon Mode","Last Run Time","Last Result","Author","Task To Run","Start In","Comment","Scheduled Task State","Idle Time","Power Management","Run As User","Delete Task If Not Rescheduled","Stop Task If Runs X Hours and X Mins","Schedule","Schedule Type","Start Time","Start Date","End Date","Days","Months","Repeat: Every","Repeat: Until: Time","Repeat: Until: Duration","Repeat: Stop If Still Running","PID"
"DESKTOP-X","\Pennywise","N/A","Running","Interactive only","5/5/2026 1:23:45 PM","0","arthur","C:\path\launcher.bat","C:\Users\arthur\.pennywise","Pennywise expense tracker","Enabled","Disabled","Stop On Battery Mode, No Start On Batteries","arthur","Disabled","Disabled","On logon","At logon time","N/A","N/A","N/A","N/A","N/A","N/A","N/A","Disabled","12345"`

	running, pid := parseTaskStatusCSV(body)
	if !running {
		t.Fatalf("expected running=true")
	}
	if pid != 12345 {
		t.Fatalf("expected pid=12345, got %d", pid)
	}
}

func TestParseTaskStatusCSV_Ready(t *testing.T) {
	body := `"HostName","TaskName","Status","PID"
"DESKTOP-X","\Pennywise","Ready",""`
	running, pid := parseTaskStatusCSV(body)
	if running {
		t.Fatalf("Ready state should not be running")
	}
	if pid != 0 {
		t.Fatalf("Ready state should have pid=0, got %d", pid)
	}
}
