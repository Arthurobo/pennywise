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

	// v2: LLM + Telegram tuning. All three have safe defaults; the user
	// never needs to set them for the app to work.
	LLMTimeout          time.Duration // PENNYWISE_LLM_TIMEOUT_SECONDS
	TelegramPollTimeout time.Duration // PENNYWISE_TELEGRAM_POLL_TIMEOUT_SECONDS
	LLMLogRetention     time.Duration // PENNYWISE_LLM_LOG_RETENTION_DAYS
}

func (c Config) Addr() string        { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
func (c Config) DBPath() string      { return filepath.Join(c.DataDir, "pennywise.db") }
func (c Config) SecretPath() string  { return filepath.Join(c.DataDir, "secret.key") }
func (c Config) PIDPath() string     { return filepath.Join(c.DataDir, "pennywise.pid") }
func (c Config) LogPath() string     { return filepath.Join(c.DataDir, "pennywise.log") }
func (c Config) IsDevelopment() bool { return strings.EqualFold(c.Env, "development") }

// Load reads configuration from environment variables, applies defaults, ensures
// the data directory exists, and loads or generates the session secret.
func Load() (Config, error) {
	dataDir := envOr("PENNYWISE_DATA_DIR", defaultDataDir())
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return Config{}, fmt.Errorf("create data dir %q: %w", dataDir, err)
	}

	port, err := strconv.Atoi(envOr("PENNYWISE_PORT", "9001"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid PENNYWISE_PORT: %w", err)
	}

	cfg := Config{
		DataDir:             dataDir,
		Host:                envOr("PENNYWISE_HOST", "127.0.0.1"),
		Port:                port,
		LogLevel:            envOr("PENNYWISE_LOG_LEVEL", "info"),
		Env:                 envOr("PENNYWISE_ENV", "production"),
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

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".pennywise")
	}
	return ".pennywise"
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
