package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_DevModeUsesLocalDev verifies that PENNYWISE_ENV=development
// flips the default data dir to ./dev so the dev DB never collides with
// the user's real install at ~/.pennywise/pennywise.db.
//
// This is the critical regression guard — anyone hacking on Pennywise
// shouldn't accidentally write to their production-style install just
// because they ran `go run` from the repo root.
func TestLoad_DevModeUsesLocalDev(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PENNYWISE_DATA_DIR", "")
	t.Setenv("PENNYWISE_ENV", "development")
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))

	// chdir into a temp directory so `./.dev` lands somewhere clean.
	prev, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := filepath.Join(tmp, "dev")
	if !strings.HasSuffix(cfg.DataDir, "dev") {
		t.Fatalf("dev DataDir: got %q, want suffix %q (cwd=%s)", cfg.DataDir, "dev", tmp)
	}
	// The path may be relative ("dev") or absolute (tmp/.dev) depending
	// on how Load resolves; both are correct as long as it's NOT the
	// user's home dir.
	abs, _ := filepath.Abs(cfg.DataDir)
	if abs != want {
		t.Fatalf("dev DataDir abs: got %q, want %q", abs, want)
	}
	if cfg.Port != 9003 {
		t.Fatalf("dev port: got %d, want 9003", cfg.Port)
	}
}

// TestLoad_ProductionModeUsesHomeDir verifies the unchanged default for
// production: ~/.pennywise.
func TestLoad_ProductionModeUsesHomeDir(t *testing.T) {
	t.Setenv("PENNYWISE_DATA_DIR", "")
	t.Setenv("PENNYWISE_ENV", "production")
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasSuffix(cfg.DataDir, ".pennywise") {
		t.Fatalf("prod DataDir: got %q, want HOME/.pennywise", cfg.DataDir)
	}
	if cfg.Port != 9002 {
		t.Fatalf("prod port: got %d, want 9002", cfg.Port)
	}
}

// TestLoad_ExplicitDataDirOverridesEverything verifies the env var still
// takes precedence in both prod and dev modes.
func TestLoad_ExplicitDataDirOverridesEverything(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("PENNYWISE_DATA_DIR", custom)
	t.Setenv("PENNYWISE_ENV", "development")
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != custom {
		t.Fatalf("explicit override ignored: got %q, want %q", cfg.DataDir, custom)
	}
}

// TestLoad_AutoDetectsDevCheckout verifies that running from inside a
// directory containing a go.mod with the right module path flips into
// development mode without the user setting PENNYWISE_ENV.
func TestLoad_AutoDetectsDevCheckout(t *testing.T) {
	tmp := t.TempDir()
	gomod := "module github.com/Arthurobo/pennywise\n\ngo 1.25.0\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("PENNYWISE_ENV", "")
	t.Setenv("PENNYWISE_DATA_DIR", "")
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "development" {
		t.Fatalf("auto-detect failed: env=%q", cfg.Env)
	}
	if !cfg.DevAutoDetected {
		t.Fatalf("DevAutoDetected should be true")
	}
	if !strings.HasSuffix(cfg.DataDir, "dev") {
		t.Fatalf("DataDir: got %q, want suffix dev", cfg.DataDir)
	}
	if cfg.Port != 9003 {
		t.Fatalf("auto-detect port: got %d, want 9003", cfg.Port)
	}
}

// TestLoad_DoesNotAutoDetectOtherProjects verifies the strict
// module-path match: a go.mod for some OTHER project in cwd does NOT
// trigger Pennywise's dev mode.
func TestLoad_DoesNotAutoDetectOtherProjects(t *testing.T) {
	tmp := t.TempDir()
	gomod := "module github.com/example/some-other-project\n\ngo 1.25.0\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("PENNYWISE_ENV", "")
	t.Setenv("PENNYWISE_DATA_DIR", "")
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "production" {
		t.Fatalf("foreign go.mod should NOT trigger dev: env=%q", cfg.Env)
	}
	if cfg.DevAutoDetected {
		t.Fatalf("DevAutoDetected should be false for foreign module")
	}
}

// TestLoad_ExplicitProductionWinsOverAutoDetect verifies that an
// explicit PENNYWISE_ENV=production overrides auto-detection. Useful
// for `pennywise start` from inside the repo against a prod DB on
// purpose (and for our smoke tests).
func TestLoad_ExplicitProductionWinsOverAutoDetect(t *testing.T) {
	tmp := t.TempDir()
	gomod := "module github.com/Arthurobo/pennywise\n\ngo 1.25.0\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("PENNYWISE_ENV", "production")
	t.Setenv("PENNYWISE_DATA_DIR", "")
	t.Setenv("PENNYWISE_SESSION_SECRET", strings.Repeat("ab", 16))
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "production" {
		t.Fatalf("explicit prod env should win: env=%q", cfg.Env)
	}
	if cfg.DevAutoDetected {
		t.Fatalf("DevAutoDetected should be false when env explicitly set")
	}
}
