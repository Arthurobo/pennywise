//go:build darwin

package cli

import (
	"strings"
	"testing"
)

func TestRenderDarwinPlist_ContainsAllRequiredKeys(t *testing.T) {
	out, err := renderDarwinPlist(darwinPlistData{
		Label:      "com.pennywise.app",
		BinaryPath: "/Users/me/go/bin/pennywise",
		HomeDir:    "/Users/me",
		LogPath:    "/Users/me/.pennywise/pennywise.log",
		EnvVars: map[string]string{
			"PENNYWISE_PORT":     "9002",
			"PENNYWISE_DATA_DIR": "/Users/me/.pennywise",
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)

	for _, want := range []string{
		"<key>Label</key>",
		"<string>com.pennywise.app</string>",
		"<string>/Users/me/go/bin/pennywise</string>",
		"<string>serve</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<true/>",
		"<key>WorkingDirectory</key>",
		"<string>/Users/me</string>",
		"<key>StandardOutPath</key>",
		"<string>/Users/me/.pennywise/pennywise.log</string>",
		"<key>EnvironmentVariables</key>",
		"<key>PENNYWISE_DATA_DIR</key>",
		"<key>PENNYWISE_PORT</key>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist missing %q\n--- full plist ---\n%s", want, body)
		}
	}
}

func TestRenderDarwinPlist_DeterministicEnvOrder(t *testing.T) {
	// Two renders with the same inputs must produce byte-identical
	// output. Map iteration is non-deterministic — we sort keys via
	// SortedEnv() — this test is the regression guard.
	data := darwinPlistData{
		Label: "com.pennywise.app", BinaryPath: "/usr/bin/pennywise",
		HomeDir: "/h", LogPath: "/h/p.log",
		EnvVars: map[string]string{
			"PENNYWISE_PORT":     "9002",
			"PENNYWISE_DATA_DIR": "/d",
			"PENNYWISE_HOST":     "127.0.0.1",
		},
	}
	a, _ := renderDarwinPlist(data)
	b, _ := renderDarwinPlist(data)
	if string(a) != string(b) {
		t.Fatalf("plist not deterministic:\na=%s\nb=%s", a, b)
	}
	// Order check: DATA_DIR before HOST before PORT (alpha).
	body := string(a)
	iData := strings.Index(body, "PENNYWISE_DATA_DIR")
	iHost := strings.Index(body, "PENNYWISE_HOST")
	iPort := strings.Index(body, "PENNYWISE_PORT")
	if !(iData < iHost && iHost < iPort) {
		t.Fatalf("env keys not alphabetically ordered: data=%d host=%d port=%d",
			iData, iHost, iPort)
	}
}

func TestRenderDarwinPlist_NoEnvVarsBlockWhenEmpty(t *testing.T) {
	out, err := renderDarwinPlist(darwinPlistData{
		Label: "x", BinaryPath: "/x", HomeDir: "/h", LogPath: "/l",
		EnvVars: map[string]string{},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(out), "EnvironmentVariables") {
		t.Fatalf("plist should not include EnvironmentVariables block when empty:\n%s", out)
	}
}

func TestParseLaunchctlListPID(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want int
	}{
		{
			name: "running",
			out: `{
	"PID" = 12345;
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.pennywise.app";
};`,
			want: 12345,
		},
		{
			name: "stopped",
			out: `{
	"PID" = -;
	"Label" = "com.pennywise.app";
};`,
			want: 0,
		},
		{
			name: "no PID field",
			out:  `{"Label" = "com.pennywise.app";}`,
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseLaunchctlListPID(c.out)
			if got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
		})
	}
}

func TestIsAlreadyLoaded(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"Bootstrap failed: 5: Input/output error", true},
		{"service already loaded", true},
		{"random other error", false},
		{"", false},
	}
	for _, c := range cases {
		got := isAlreadyLoaded(c.out)
		if got != c.want {
			t.Fatalf("isAlreadyLoaded(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}
