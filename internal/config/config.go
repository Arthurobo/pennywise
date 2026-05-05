// Package config loads runtime configuration from environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DataDir       string
	Host          string
	Port          int
	SessionSecret []byte
	LogLevel      string
	Env           string

	// DevAutoDetected is true when Load identified the current working
	// directory as a checkout of this module (go.mod with the right
	// module path) and flipped Env to "development" without the user
	// explicitly setting PENNYWISE_ENV. Server startup logs this so the
	// reason "I'm using ./.dev/pennywise.db" is always visible.
	DevAutoDetected bool

	// v2: LLM + Telegram tuning. All three have safe defaults; the user
	// never needs to set them for the app to work.
	LLMTimeout          time.Duration // PENNYWISE_LLM_TIMEOUT_SECONDS
	TelegramPollTimeout time.Duration // PENNYWISE_TELEGRAM_POLL_TIMEOUT_SECONDS
	LLMLogRetention     time.Duration // PENNYWISE_LLM_LOG_RETENTION_DAYS
}

// modulePath is the canonical Go import path of this module. Used by
// inDevCheckout() to recognize that we're running from inside this
// project's source tree (vs. a globally-installed binary in some other
// cwd).
const modulePath = "github.com/Arthurobo/pennywise"

func (c Config) Addr() string        { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
func (c Config) DBPath() string      { return filepath.Join(c.DataDir, "pennywise.db") }
func (c Config) SecretPath() string  { return filepath.Join(c.DataDir, "secret.key") }
func (c Config) PIDPath() string     { return filepath.Join(c.DataDir, "pennywise.pid") }
func (c Config) LogPath() string     { return filepath.Join(c.DataDir, "pennywise.log") }
func (c Config) IsDevelopment() bool { return strings.EqualFold(c.Env, "development") }

// Load reads configuration from environment variables, applies defaults, ensures
// the data directory exists, and loads or generates the session secret.
//
// Dev auto-detection: when PENNYWISE_ENV is unset AND the current working
// directory is a checkout of this module (go.mod present and matches
// modulePath), Env is implicitly set to "development". This means
// `./pennywise` from inside the repo "just works" with a local DB at
// ./.dev/pennywise.db, never colliding with the user's real install at
// ~/.pennywise/pennywise.db. Outside the repo, behavior is unchanged.
func Load() (Config, error) {
	envExplicit := os.Getenv("PENNYWISE_ENV")
	env := envExplicit
	if env == "" {
		env = "production"
	}
	devAutoDetected := false
	if envExplicit == "" && inDevCheckout() {
		env = "development"
		devAutoDetected = true
	}

	// Dev-mode DataDir default: ./.dev (cwd-relative) so the dev DB lives
	// inside the repo, gitignored, never colliding with ~/.pennywise.
	dataDir := envOr("PENNYWISE_DATA_DIR", defaultDataDirFor(env))
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return Config{}, fmt.Errorf("create data dir %q: %w", dataDir, err)
	}

	port, err := strconv.Atoi(envOr("PENNYWISE_PORT", defaultPortFor(env)))
	if err != nil {
		return Config{}, fmt.Errorf("invalid PENNYWISE_PORT: %w", err)
	}

	cfg := Config{
		DataDir:             dataDir,
		Host:                envOr("PENNYWISE_HOST", "127.0.0.1"),
		Port:                port,
		LogLevel:            envOr("PENNYWISE_LOG_LEVEL", "info"),
		Env:                 env,
		DevAutoDetected:     devAutoDetected,
		LLMTimeout:          envSeconds("PENNYWISE_LLM_TIMEOUT_SECONDS", 30),
		TelegramPollTimeout: envSeconds("PENNYWISE_TELEGRAM_POLL_TIMEOUT_SECONDS", 30),
		LLMLogRetention:     envDays("PENNYWISE_LLM_LOG_RETENTION_DAYS", 30),
	}

	cfg.SessionSecret, err = loadOrCreateSecret(cfg.SecretPath(), os.Getenv("PENNYWISE_SESSION_SECRET"))
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envSeconds(key string, fallback int) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return time.Duration(fallback) * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return time.Duration(fallback) * time.Second
	}
	return time.Duration(n) * time.Second
}

func envDays(key string, fallback int) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return time.Duration(fallback) * 24 * time.Hour
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return time.Duration(fallback) * 24 * time.Hour
	}
	return time.Duration(n) * 24 * time.Hour
}

// defaultDataDirFor returns the right default data directory for the
// given environment. Production lands in ~/.pennywise; development lands
// in ./dev (relative to cwd) so the dev DB stays inside the repo,
// gitignored, separate from the user's real install.
func defaultDataDirFor(env string) string {
	if strings.EqualFold(env, "development") {
		return "dev"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".pennywise")
	}
	return ".pennywise"
}

// defaultPortFor returns the default HTTP port for the given environment.
// Dev (9003) and production (9002) use different ports so a developer
// can run `pennywise start` for their real install AND `./pennywise`
// from inside the repo simultaneously without a bind collision.
func defaultPortFor(env string) string {
	if strings.EqualFold(env, "development") {
		return "9003"
	}
	return "9002"
}

// inDevCheckout reports whether cwd contains a go.mod whose first
// `module ...` line names this exact module. The strict module-path
// match is the safety: a stray cd into some other Go project doesn't
// fool Pennywise into using ./.dev. Only inside a checkout of this
// project does dev mode auto-trigger.
func inDevCheckout() bool {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "module ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			path = strings.Trim(path, "\"")
			return path == modulePath
		}
		// `module` is required to appear before any other directive in
		// a valid go.mod, so if we hit a different directive first the
		// file is malformed — bail.
		return false
	}
	return false
}

// loadOrCreateSecret returns the user-supplied secret if non-empty; otherwise
// loads the persisted secret from path, generating a new one on first run.
func loadOrCreateSecret(path, override string) ([]byte, error) {
	if override != "" {
		b, err := hex.DecodeString(override)
		if err != nil {
			return nil, fmt.Errorf("PENNYWISE_SESSION_SECRET must be hex-encoded: %w", err)
		}
		if len(b) < 16 {
			return nil, fmt.Errorf("PENNYWISE_SESSION_SECRET must be at least 16 bytes (32 hex chars)")
		}
		return b, nil
	}

	// path is built from PENNYWISE_DATA_DIR which the operator controls; this
	// is a server-managed path, not user input.
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // see comment above
		b, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, fmt.Errorf("session secret at %s is corrupt: %w", path, err)
		}
		return b, nil
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	encoded := hex.EncodeToString(secret)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("persist session secret: %w", err)
	}
	return secret, nil
}
