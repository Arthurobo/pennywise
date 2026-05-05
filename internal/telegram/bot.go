package telegram

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// Bot is one running Telegram bot session. The supervisor creates one when
// both LLM + Telegram configs are enabled and tears it down otherwise.
//
// The Bot owns: the long-poll loop, the dispatcher, an in-memory chat-ignore
// set (for "this bot is connected to someone else's Pennywise" scenarios),
// and the in-memory edit-state map (for the Edit-via-reply callback flow).
type Bot struct {
	client      *Client
	username    string
	q           *sqlcgen.Queries
	db          *sql.DB
	secrets     *auth.SecretBox
	llmTimeout  time.Duration
	pollTimeout time.Duration

	dispatcher *Dispatcher
}

type botOpts struct {
	Token       string
	Username    string
	Q           *sqlcgen.Queries
	DB          *sql.DB
	Secrets     *auth.SecretBox
	LLMTimeout  time.Duration
	PollTimeout time.Duration
}

func newBot(o botOpts) *Bot {
	client := NewClient(o.Token, nil)
	b := &Bot{
		client:      client,
		username:    o.Username,
		q:           o.Q,
		db:          o.DB,
		secrets:     o.Secrets,
		llmTimeout:  o.LLMTimeout,
		pollTimeout: o.PollTimeout,
	}
	b.dispatcher = NewDispatcher(DispatcherOpts{
		Client:     client,
		Q:          o.Q,
		DB:         o.DB,
		BotUsername: o.Username,
		LLMTimeout: o.LLMTimeout,
		Secrets:    o.Secrets,
	})
	return b
}

// Run is the bot's main loop. It returns nil only when ctx is canceled.
//
// Before entering the polling loop it registers the slash-command menu and
// bot description with Telegram. These calls are idempotent — running them
// on every start keeps the menu consistent across version bumps without
// requiring any BotFather interaction. Failures are logged but non-fatal.
func (b *Bot) Run(ctx context.Context) error {
	b.registerPresence(ctx)
	return b.poll(ctx)
}

// registerPresence calls setMyCommands / setMyDescription / setMyShortDescription.
// All three are best-effort: a failure means the bot's autocomplete might be
// stale, not that the bot can't run.
func (b *Bot) registerPresence(ctx context.Context) {
	if err := b.client.SetMyCommands(ctx, defaultBotCommands); err != nil {
		slog.Warn("telegram: setMyCommands failed", "error", err)
	}
	if err := b.client.SetMyDescription(ctx, defaultBotDescription); err != nil {
		slog.Warn("telegram: setMyDescription failed", "error", err)
	}
	if err := b.client.SetMyShortDescription(ctx, defaultBotShortDescription); err != nil {
		slog.Warn("telegram: setMyShortDescription failed", "error", err)
	}
}
