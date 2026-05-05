package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/config"
	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/handlers"
	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/telegram"
	"github.com/Arthurobo/pennywise/internal/templates"
)

// VersionInfo carries build-time identifiers injected via -ldflags.
type VersionInfo struct {
	Version, Commit, BuildDate string
}

// Run boots the database, builds the handler, and serves HTTP until signaled.
func Run(ctx context.Context, cfg config.Config, vi VersionInfo) error {
	configureLogger(cfg)

	db, err := pwdb.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	q := sqlcgen.New(db)
	csrf := auth.NewCSRF(cfg.SessionSecret)
	sessions := auth.NewManager(q)
	sessions.StartSweeper(ctx)

	secrets, err := auth.NewSecretBox(cfg.SessionSecret)
	if err != nil {
		return fmt.Errorf("secret box: %w", err)
	}

	// LLM call log retention sweeper.
	llm.StartLogSweeper(ctx, q, cfg.LLMLogRetention)

	// Trash sweeper: hard-deletes expenses that have been in the trash
	// longer than owner.trash_retention_days (1..365, default 30).
	handlers.StartTrashSweeper(ctx, q)

	// Telegram supervisor: starts/stops the bot when LLM + Telegram configs
	// are valid + enabled. Runs in its own goroutine; handler triggers it on
	// config changes so setup feels snappy.
	supervisor := telegram.NewSupervisor(telegram.SupervisorOpts{
		DB:           db,
		Q:            q,
		Secrets:      secrets,
		LLMTimeout:   cfg.LLMTimeout,
		PollTimeout:  cfg.TelegramPollTimeout,
	})
	supervisor.Start(ctx)

	h := handlers.New(cfg, db, q, nil, sessions, csrf, secrets, supervisor,
		vi.Version, vi.Commit, vi.BuildDate)
	if err := h.WarmInitFlag(ctx); err != nil {
		return fmt.Errorf("init flag: %w", err)
	}

	rdr, err := templates.New(cfg.IsDevelopment(), h.TemplateFuncs(), nil)
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}
	h.Renderer = rdr

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           Mount(h),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	stopCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("pennywise starting", "addr", cfg.Addr(), "env", cfg.Env, "version", vi.Version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-stopCtx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("shutdown", "error", err)
	}
	return nil
}

func configureLogger(cfg config.Config) {
	var lvl slog.Level
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(h))
}
