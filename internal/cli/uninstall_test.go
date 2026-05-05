package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUninstall_CancelOnNoConfirmation drives the command with
// non-confirming stdin ("no\n") and asserts that no destruction
// happens. This is the only end-to-end test it's safe to run — the
// happy path actually deletes files, which would clobber the test
// host's $HOME if we ran it carelessly.
func TestUninstall_CancelOnNoConfirmation(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PENNYWISE_DATA_DIR", dataDir)
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))
	// Drop a sentinel file so we can verify nothing got deleted.
	sentinelPath := filepath.Join(dataDir, "sentinel.txt")
	if err := os.WriteFile(sentinelPath, []byte("still here"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	out := &bytes.Buffer{}
	cmd := newUninstallCommand()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader("no\n"))
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}

	body := out.String()
	if !strings.Contains(body, "Cancelled") {
		t.Fatalf("expected cancellation message; got %q", body)
	}
	if _, err := os.ReadFile(sentinelPath); err != nil {
		t.Fatalf("sentinel was deleted on cancel; expected it to still exist: %v", err)
	}
}

// TestUninstall_PromptListsTargets asserts that the confirmation prompt
// names the data dir + binary so the user sees exactly what's about to
// disappear before typing 'yes'.
func TestUninstall_PromptListsTargets(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PENNYWISE_DATA_DIR", dataDir)
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))

	out := &bytes.Buffer{}
	cmd := newUninstallCommand()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader("\n")) // empty line → cancel
	_ = cmd.ExecuteContext(context.Background())

	body := out.String()
	for _, want := range []string{
		"This will permanently delete",
		"OS service",
		"Data directory: " + dataDir,
		"Installed binary",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q\n--- full output ---\n%s", want, body)
		}
	}
}

