package cli

import (
	"strings"
	"testing"
)

func TestPennywiseEnv_FiltersToPENNYWISEPrefix(t *testing.T) {
	t.Setenv("PENNYWISE_PORT", "9002")
	t.Setenv("PENNYWISE_DATA_DIR", "/tmp/pw-test")
	t.Setenv("HOME", "/Users/test")
	t.Setenv("UNRELATED_VAR", "noise")

	got := pennywiseEnv()

	if got["PENNYWISE_PORT"] != "9002" {
		t.Fatalf("missing PENNYWISE_PORT: %+v", got)
	}
	if got["PENNYWISE_DATA_DIR"] != "/tmp/pw-test" {
		t.Fatalf("missing PENNYWISE_DATA_DIR: %+v", got)
	}
	if _, ok := got["HOME"]; ok {
		t.Fatalf("HOME should be filtered out: %+v", got)
	}
	if _, ok := got["UNRELATED_VAR"]; ok {
		t.Fatalf("UNRELATED_VAR should be filtered out: %+v", got)
	}
}

func TestSortedKeys_Deterministic(t *testing.T) {
	in := map[string]string{
		"PENNYWISE_PORT":     "9002",
		"PENNYWISE_DATA_DIR": "/tmp",
		"PENNYWISE_HOST":     "127.0.0.1",
	}
	got := sortedKeys(in)
	want := []string{"PENNYWISE_DATA_DIR", "PENNYWISE_HOST", "PENNYWISE_PORT"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sortedKeys mismatch: got %v want %v", got, want)
	}
}

func TestServiceLabel_Constant(t *testing.T) {
	// Pin the canonical name so a future rename forces a deliberate
	// update in this test (and in the user's existing plist/unit files
	// that may reference it).
	if ServiceLabel != "com.pennywise.app" {
		t.Fatalf("ServiceLabel changed: %q", ServiceLabel)
	}
}
