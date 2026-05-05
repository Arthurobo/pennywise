// Package telegram drives the Telegram bot: long polling, message dispatch,
// inline-keyboard callbacks, and the supervisor that starts/stops the bot
// based on llm_config and telegram_config.
//
// Two non-negotiable principles inherited from the v2 spec:
//
//   - BYOB. Each user creates their own bot via @BotFather and provides a
//     token. No central infrastructure.
//   - BYOLLM. The bot calls the user's configured LLM directly. Pennywise
//     never proxies these calls.
//
// The supervisor owns the bot's lifecycle. It runs in its own goroutine,
// reconciles config every 30 seconds, and reacts immediately to a
// Trigger() poke (used by the settings handlers to make setup snappy).
package telegram

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// SupervisorOpts is the dependency bundle the supervisor needs to spin up
// a bot when configuration becomes valid.
type SupervisorOpts struct {
	DB          *sql.DB
	Q           *sqlcgen.Queries
	Secrets     *auth.SecretBox
	LLMTimeout  time.Duration
	PollTimeout time.Duration
}

// Supervisor reconciles bot lifecycle against the database state.
type Supervisor struct {
	opts SupervisorOpts

	mu        sync.Mutex
	running   *Bot
	runCancel context.CancelFunc

	triggerCh chan struct{}
}

// NewSupervisor wires up dependencies but does not start the loop. Call
// Start to begin reconciliation.
func NewSupervisor(opts SupervisorOpts) *Supervisor {
	return &Supervisor{
		opts:      opts,
		triggerCh: make(chan struct{}, 1),
	}
}

// Trigger asks the supervisor to reconcile immediately rather than waiting
// for the next tick. Safe to call from any goroutine; non-blocking.
func (s *Supervisor) Trigger() {
	select {
	case s.triggerCh <- struct{}{}:
	default:
	}
}

// Start launches the reconciliation loop in a goroutine. ctx cancellation
// stops the loop and any running bot.
func (s *Supervisor) Start(ctx context.Context) {
	go s.loop(ctx)
}

func (s *Supervisor) loop(ctx context.Context) {
	s.reconcile(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stop()
			return
		case <-t.C:
			s.reconcile(ctx)
		case <-s.triggerCh:
			s.reconcile(ctx)
		}
	}
}

// reconcile starts the bot if it should be running, stops it otherwise.
// Idempotent — safe to call from multiple paths.
//
// "Should be running" rule:
//   - LLM provider must be configured and enabled.
//   - Telegram bot row must exist (i.e. token saved).
//   - If the bot is UNPAIRED (chat_id IS NULL), it must run so it can
//     receive the /start <code> message that completes pairing. The
//     `enabled` flag is meaningless in that state.
//   - If the bot is PAIRED, the user's `enabled` toggle controls it.
func (s *Supervisor) reconcile(ctx context.Context) {
	llm, tg, ok := s.loadConfigs(ctx)
	shouldRun := ok &&
		llm.Enabled == 1 &&
		(!tg.ChatID.Valid || tg.Enabled == 1)

	s.mu.Lock()
	defer s.mu.Unlock()

	if shouldRun && s.running == nil {
		token, err := s.opts.Secrets.Open(tg.BotTokenEncrypted)
		if err != nil {
			slog.Warn("telegram: open bot token", "error", err)
			return
		}
		botCtx, cancel := context.WithCancel(ctx)
		bot := newBot(botOpts{
			Token:        token,
			Username:     tg.BotUsername,
			Q:            s.opts.Q,
			DB:           s.opts.DB,
			Secrets:      s.opts.Secrets,
			LLMTimeout:   s.opts.LLMTimeout,
			PollTimeout:  s.opts.PollTimeout,
		})
		s.running = bot
		s.runCancel = cancel
		go func() {
			if err := bot.Run(botCtx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("telegram bot exited", "error", err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.running == bot {
				s.running = nil
				s.runCancel = nil
			}
		}()
		slog.Info("telegram bot started", "username", tg.BotUsername)
		return
	}
	if !shouldRun && s.running != nil {
		s.runCancelLocked()
	}
}

func (s *Supervisor) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runCancelLocked()
}

func (s *Supervisor) runCancelLocked() {
	if s.runCancel != nil {
		s.runCancel()
		s.runCancel = nil
		s.running = nil
		slog.Info("telegram bot stopped")
	}
}

// loadConfigs returns both rows together. ok is false if either row is
// missing or query fails — i.e., the bot can't run yet.
func (s *Supervisor) loadConfigs(ctx context.Context) (llm sqlcgen.LlmConfig, tg sqlcgen.TelegramConfig, ok bool) {
	llm, err := s.opts.Q.GetLLMConfig(ctx)
	if err != nil {
		return sqlcgen.LlmConfig{}, sqlcgen.TelegramConfig{}, false
	}
	tg, err = s.opts.Q.GetTelegramConfig(ctx)
	if err != nil {
		return sqlcgen.LlmConfig{}, sqlcgen.TelegramConfig{}, false
	}
	return llm, tg, true
}
