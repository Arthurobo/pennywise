//go:build linux

package cli

import (
	"strings"
	"testing"
)

func TestRenderLinuxUnit_ContainsRequiredSections(t *testing.T) {
	out, err := renderLinuxUnit(linuxUnitData{
		BinaryPath: "/home/me/go/bin/pennywise",
		EnvVars: map[string]string{
			"PENNYWISE_PORT":     "9002",
			"PENNYWISE_DATA_DIR": "/home/me/.pennywise",
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)

	for _, want := range []string{
		"[Unit]",
		"Description=Pennywise expense tracker",
		"After=network.target",
		"[Service]",
		"Type=simple",
		"ExecStart=/home/me/go/bin/pennywise serve",
		"Restart=on-failure",
		"RestartSec=5s",
		"Environment=PENNYWISE_DATA_DIR=/home/me/.pennywise",
		"Environment=PENNYWISE_PORT=9002",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("unit missing %q\n--- full unit ---\n%s", want, body)
		}
	}
}

func TestRenderLinuxUnit_DeterministicEnvOrder(t *testing.T) {
	data := linuxUnitData{
		BinaryPath: "/x",
		EnvVars: map[string]string{
			"PENNYWISE_PORT":     "9002",
			"PENNYWISE_DATA_DIR": "/d",
			"PENNYWISE_HOST":     "127.0.0.1",
		},
	}
	a, _ := renderLinuxUnit(data)
	b, _ := renderLinuxUnit(data)
	if string(a) != string(b) {
		t.Fatalf("unit not deterministic:\na=%s\nb=%s", a, b)
	}
	body := string(a)
	iData := strings.Index(body, "Environment=PENNYWISE_DATA_DIR")
	iHost := strings.Index(body, "Environment=PENNYWISE_HOST")
	iPort := strings.Index(body, "Environment=PENNYWISE_PORT")
	if !(iData < iHost && iHost < iPort) {
		t.Fatalf("Environment lines not alphabetically ordered: data=%d host=%d port=%d",
			iData, iHost, iPort)
	}
}

func TestRenderLinuxUnit_NoEnvLinesWhenEmpty(t *testing.T) {
	out, err := renderLinuxUnit(linuxUnitData{
		BinaryPath: "/x",
		EnvVars:    map[string]string{},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(out), "Environment=") {
		t.Fatalf("unit should not include Environment= when empty:\n%s", out)
	}
}
