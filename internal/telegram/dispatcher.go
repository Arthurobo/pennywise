package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/fuzzy"
	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/models"
)

// ─── Types ─────────────────────────────────────────────────────────────────

type DispatcherOpts struct {
	Client      *Client
	Q           *sqlcgen.Queries
	DB          *sql.DB
	BotUsername string
	LLMTimeout  time.Duration
	Secrets     *auth.SecretBox

	// LLMProvider, when non-nil, is used by loadLLM instead of building a
	// real provider via llm.Factory. Test-only seam — production callers
	// must leave this nil so the configured provider + decrypted API key
	// are used. Allows the dispatcher tests to inject a MockProvider with
	// canned responses without having to round-trip through encryption.
	LLMProvider llm.Provider
}

// followUpKind describes what the bot expects from the user's NEXT TEXT MESSAGE.
type followUpKind int

const (
	fuEdit              followUpKind = iota + 1 // re-parse as a correction to ExpenseID
	fuNewLedgerName                              // user is typing a new ledger name
	fuNewCategoryName                            // user is typing a new category name
	fuPendingEdit                                // user is typing a correction to a low-confidence pending parse (not yet in DB)
)

// followUp is one row in d.followUps, keyed by chat ID.
type followUp struct {
	Kind        followUpKind
	ExpenseID   int64 // 0 if not tied to an expense
	OrigMsgID   int64 // confirmation message_id to edit in place after the create
	PromptMsgID int64 // the prompt message we keep editing in place through the flow
}

// pendingConfirmKind is the action awaiting the user's Yes/Cancel tap.
type pendingConfirmKind int

const (
	pcCreateLedger   pendingConfirmKind = iota + 1
	pcCreateCategory
)

// pendingConfirm is one row in d.pendingConfirms, keyed by chat ID.
type pendingConfirm struct {
	Kind        pendingConfirmKind
	Name        string
	ExpenseID   int64 // 0 if not tied to an expense (slash-command flow)
	OrigMsgID   int64 // confirmation message to edit in place after the create
	PromptMsgID int64 // the "Create '<name>'?" message we edit on resolution
	SetSticky   bool  // ledger only: also set as sticky context after create
}

// Dispatcher is the message router. One per running Bot.
type Dispatcher struct {
	opts DispatcherOpts

	mu               sync.Mutex
	ignoredChats     map[int64]bool
	followUps        map[int64]followUp        // chat_id → expected next-text behaviour
	pendingConfirms  map[int64]pendingConfirm  // chat_id → expected next-tap (Yes/Cancel)
	pendingExpenses  map[int64]pendingExpense  // chat_id → low-confidence parse awaiting Yes/Edit/Cancel

	// Batch entry: undo tokens are short keys baked into the [↩ Undo all]
	// callback data. The map stores the expense IDs to delete on tap.
	batchUndosMu  sync.Mutex
	batchUndos    map[string][]int64
	batchTokenSeq atomic.Int64
}

func NewDispatcher(o DispatcherOpts) *Dispatcher {
	return &Dispatcher{
		opts:            o,
		ignoredChats:    map[int64]bool{},
		followUps:       map[int64]followUp{},
		pendingConfirms: map[int64]pendingConfirm{},
		pendingExpenses: map[int64]pendingExpense{},
		batchUndos:      map[string][]int64{},
	}
}

// ─── Top-level dispatch ────────────────────────────────────────────────────

func (d *Dispatcher) Dispatch(ctx context.Context, u Update) {
	if u.Message != nil {
		d.handleMessage(ctx, u.Message)
		return
	}
	if u.CallbackQuery != nil {
		d.handleCallback(ctx, u.CallbackQuery)
		return
	}
}

func (d *Dispatcher) handleMessage(ctx context.Context, m *Message) {
	if m == nil || m.Chat.ID == 0 {
		return
	}
	chatID := m.Chat.ID

	d.mu.Lock()
	ignored := d.ignoredChats[chatID]
	d.mu.Unlock()
	if ignored {
		return
	}

	tg, err := d.opts.Q.GetTelegramConfig(ctx)
	if err != nil {
		slog.Warn("telegram: load config", "error", err)
		return
	}

	switch {
	case !tg.ChatID.Valid:
		d.handleUnpaired(ctx, m)
		return
	case tg.ChatID.Int64 != chatID:
		d.mu.Lock()
		d.ignoredChats[chatID] = true
		d.mu.Unlock()
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"This bot is connected to someone else's Pennywise. Goodbye.", nil)
		return
	}

	if m.Voice != nil || m.Audio != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Voice notes aren't supported yet — coming in v2.5. Please type the expense.", nil)
		return
	}
	// Image-bearing messages: photos always, plus documents whose MIME
	// matches a supported receipt format (PDF, HEIC, etc.). The handler
	// rejects unknown document types with its own message.
	if len(m.Photo) > 0 || (m.Document != nil && isReceiptDocument(m.Document)) {
		d.handlePhoto(ctx, m)
		return
	}

	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}

	// /cancel always wins: clears any pending state and acknowledges.
	if strings.EqualFold(text, "/cancel") {
		d.cancelAllPending(ctx, chatID)
		return
	}

	// Pending follow-up state machine: if we asked the user to type something,
	// this message is that something — UNLESS they typed a slash command, in
	// which case bail out of the follow-up and run the command instead.
	d.mu.Lock()
	fu, hasFollowUp := d.followUps[chatID]
	if hasFollowUp && !strings.HasPrefix(text, "/") {
		delete(d.followUps, chatID)
	}
	d.mu.Unlock()
	if hasFollowUp && !strings.HasPrefix(text, "/") {
		switch fu.Kind {
		case fuEdit:
			d.handleEditReply(ctx, m, fu)
		case fuNewLedgerName:
			d.handleNewName(ctx, m, fu, pcCreateLedger)
		case fuNewCategoryName:
			d.handleNewName(ctx, m, fu, pcCreateCategory)
		case fuPendingEdit:
			d.handlePendingEditReply(ctx, m, fu)
		}
		return
	}

	if strings.HasPrefix(text, "/") {
		d.handleCommand(ctx, m, text)
		return
	}
	d.handleFreeText(ctx, m, text)
}

// cancelAllPending clears every transient state for this chat.
func (d *Dispatcher) cancelAllPending(ctx context.Context, chatID int64) {
	d.mu.Lock()
	hadFU := false
	if _, ok := d.followUps[chatID]; ok {
		delete(d.followUps, chatID)
		hadFU = true
	}
	if _, ok := d.pendingConfirms[chatID]; ok {
		delete(d.pendingConfirms, chatID)
		hadFU = true
	}
	if _, ok := d.pendingExpenses[chatID]; ok {
		delete(d.pendingExpenses, chatID)
		hadFU = true
	}
	d.mu.Unlock()
	if hadFU {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Cancelled.", nil)
	} else {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Nothing to cancel.", nil)
	}
}

// ─── Pairing ────────────────────────────────────────────────────────────────

func (d *Dispatcher) handleUnpaired(ctx context.Context, m *Message) {
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	if !strings.HasPrefix(text, "/start") {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Please send your pairing code: /start PW-XXXXXX", nil)
		return
	}
	parts := strings.Fields(text)
	if len(parts) < 2 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Please provide your pairing code: /start PW-XXXXXX", nil)
		return
	}
	code := NormalizePairingInput(parts[1])

	tg, err := d.opts.Q.GetTelegramConfig(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Something went wrong on the server. Please try again.", nil)
		return
	}
	if !tg.PairingCode.Valid || tg.PairingCode.String == "" {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"No pairing code is active. Generate one in Pennywise → Settings → Telegram Bot.", nil)
		return
	}
	if !tg.PairingExpiresAt.Valid || tg.PairingExpiresAt.Int64 < time.Now().Unix() {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Invalid or expired pairing code.", nil)
		return
	}
	if !strings.EqualFold(tg.PairingCode.String, code) {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Invalid or expired pairing code.", nil)
		return
	}

	if err := d.opts.Q.SetTelegramChatID(ctx, sqlcgen.SetTelegramChatIDParams{
		ChatID:    sql.NullInt64{Int64: chatID, Valid: true},
		UpdatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		slog.Warn("telegram: persist chat_id", "error", err)
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Pairing failed on the server. Please try again.", nil)
		return
	}

	owner, _ := d.opts.Q.GetOwner(ctx)
	lines := []string{
		"✅ *Pairing successful.*",
		"You can now log expenses by texting me in plain language.",
		"",
		"*Try one of these:*",
		"`5000 fuel`",
		"`12.50 coffee`",
		"`30 groceries yesterday`",
		"",
		"Type `/` to see all commands, or tap the menu button next to the input.",
	}
	if hint := dashboardURLHint(owner); hint != "" {
		lines = append(lines, "", hint)
	}
	_, _ = d.opts.Client.SendMessage(ctx, chatID, strings.Join(lines, "\n"), &SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: helpQuickActions(),
	})
}

// ─── Slash commands ────────────────────────────────────────────────────────

func (d *Dispatcher) handleCommand(ctx context.Context, m *Message, text string) {
	chatID := m.Chat.ID
	parts := strings.Fields(text)
	cmd := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if i := strings.Index(cmd, "@"); i >= 0 {
		cmd = cmd[:i]
	}
	args := parts[1:]

	switch cmd {
	case "start":
		owner, _ := d.opts.Q.GetOwner(ctx)
		body := "This bot is already paired. Try logging an expense like `5000 fuel`, or `/help`."
		if hint := dashboardURLHint(owner); hint != "" {
			body += "\n\n" + hint
		}
		_, _ = d.opts.Client.SendMessage(ctx, chatID, body,
			&SendMessageOpts{ParseMode: "Markdown"})
	case "help":
		d.cmdHelp(ctx, chatID)
	case "today":
		d.cmdSummary(ctx, chatID, "today")
	case "week":
		d.cmdSummary(ctx, chatID, "week")
	case "month":
		d.cmdSummary(ctx, chatID, "month")
	case "year":
		d.cmdSummary(ctx, chatID, "year")
	case "last":
		n := 5
		if len(args) > 0 {
			if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
				n = v
			}
		}
		d.cmdLast(ctx, chatID, n)
	case "undo":
		d.cmdUndo(ctx, chatID)
	case "ledgers":
		d.cmdLedgers(ctx, chatID)
	case "ledger":
		d.cmdLedger(ctx, chatID, args)
	case "categories":
		d.cmdCategories(ctx, chatID)
	case "category":
		d.cmdCategory(ctx, chatID, args)
	case "cancel":
		d.cancelAllPending(ctx, chatID)
	default:
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Unknown command. Try /help.", nil)
	}
}

func (d *Dispatcher) cmdHelp(ctx context.Context, chatID int64) {
	owner, _ := d.opts.Q.GetOwner(ctx)
	body := helpText()
	if hint := dashboardURLHint(owner); hint != "" {
		body += "\n\n" + hint
	}
	_, _ = d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: helpQuickActions(),
	})
}

// cmdSummary handles /today /week /month /year — total + count for the period.
func (d *Dispatcher) cmdSummary(ctx context.Context, chatID int64, period string) {
	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load your profile.", nil)
		return
	}
	loc := models.LoadLocation(owner.Timezone)
	now := time.Now().In(loc)

	var from, to time.Time
	var label string
	switch period {
	case "today":
		from, to, label = startOfDay(now, loc), startOfDay(now, loc).AddDate(0, 0, 1), "Today"
	case "week":
		from = startOfWeek(now, loc)
		to = from.AddDate(0, 0, 7)
		label = "This week"
	case "month":
		from = startOfMonth(now, loc)
		to = from.AddDate(0, 1, 0)
		label = now.Format("January 2006")
	case "year":
		from = startOfYear(now, loc)
		to = from.AddDate(1, 0, 0)
		label = now.Format("2006")
	default:
		return
	}

	row, err := d.opts.Q.SummaryBetween(ctx, sqlcgen.SummaryBetweenParams{
		SpentAt: from.Unix(), SpentAt_2: to.Unix(),
	})
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't compute the summary.", nil)
		return
	}

	body := fmt.Sprintf("📊 *%s*\n\n%s — %d %s",
		EscapeMarkdown(label),
		EscapeMarkdown(models.FormatMoney(row.Total, owner.CurrencySymbol)),
		row.ExpenseCount,
		pluralExpense(row.ExpenseCount))
	_, _ = d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{ParseMode: "Markdown"})
}

func (d *Dispatcher) cmdLast(ctx context.Context, chatID int64, n int) {
	if n < 1 {
		n = 5
	}
	if n > 20 {
		n = 20
	}
	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load your profile.", nil)
		return
	}
	loc := models.LoadLocation(owner.Timezone)

	rows, err := d.opts.Q.ListRecentExpenses(ctx, int64(n))
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load expenses.", nil)
		return
	}
	if len(rows) == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "No expenses yet. Try `5000 fuel`.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("📜 *Last %d %s*\n\n", len(rows), pluralExpense(int64(len(rows)))))
	for _, r := range rows {
		date := relativeDateLabel(time.Unix(r.SpentAt, 0), loc)
		amount := models.FormatMoney(r.Amount, owner.CurrencySymbol)
		cat := ""
		if r.CategoryName.Valid && r.CategoryName.String != "" {
			cat = " · 🏷 " + EscapeMarkdown(r.CategoryName.String)
		}
		b.WriteString(fmt.Sprintf("• *%s* — %s\n  📅 %s%s\n",
			EscapeMarkdown(amount),
			EscapeMarkdown(r.Description),
			EscapeMarkdown(date),
			cat))
	}
	_, _ = d.opts.Client.SendMessage(ctx, chatID, b.String(), &SendMessageOpts{ParseMode: "Markdown"})
}

func (d *Dispatcher) cmdUndo(ctx context.Context, chatID int64) {
	owner, _ := d.opts.Q.GetOwner(ctx)
	rows, err := d.opts.Q.ListRecentExpenses(ctx, 1)
	if err != nil || len(rows) == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Nothing to undo.", nil)
		return
	}
	last := rows[0]
	now := time.Now().UTC().Unix()
	if err := d.opts.Q.SoftDeleteExpense(ctx, sqlcgen.SoftDeleteExpenseParams{
		DeletedAt: sql.NullInt64{Int64: now, Valid: true},
		UpdatedAt: now,
		ID:        last.ID,
	}); err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't delete the expense.", nil)
		return
	}
	body := fmt.Sprintf("🗑 Deleted: *%s* — %s",
		EscapeMarkdown(models.FormatMoney(last.Amount, owner.CurrencySymbol)),
		EscapeMarkdown(last.Description))
	_, _ = d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{ParseMode: "Markdown"})
}

func (d *Dispatcher) cmdLedgers(ctx context.Context, chatID int64) {
	leds, err := d.opts.Q.ListActiveLedgers(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load ledgers.", nil)
		return
	}
	tg, _ := d.opts.Q.GetTelegramConfig(ctx)

	var b strings.Builder
	b.WriteString("📒 *Your ledgers*\n\n")
	if len(leds) == 0 {
		b.WriteString("_None yet. Add one with_ `/ledger new <name>`")
	} else {
		for _, l := range leds {
			marker := ""
			if tg.ActiveLedgerID.Valid && l.ID == tg.ActiveLedgerID.Int64 {
				marker = " ⭐ _active_"
			}
			b.WriteString(fmt.Sprintf("• %s%s\n", EscapeMarkdown(l.Name), marker))
		}
		b.WriteString("\nUse `/ledger <name>` to set sticky context, `/ledger off` to clear,")
		b.WriteString("\n`/ledger new <name>` to add a new one.")
	}
	_, _ = d.opts.Client.SendMessage(ctx, chatID, b.String(), &SendMessageOpts{ParseMode: "Markdown"})
}

// cmdLedger dispatches the three /ledger sub-flows:
//
//   /ledger off            → clear sticky context, unpin
//   /ledger new <name>     → confirm-prompt to create
//   /ledger <name>         → set sticky to existing fuzzy match;
//                            on miss, confirm-prompt to create
func (d *Dispatcher) cmdLedger(ctx context.Context, chatID int64, args []string) {
	if len(args) == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Usage: `/ledger <name>` to set sticky, `/ledger off` to clear, `/ledger new <name>` to add. See `/ledgers` for the list.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	first := strings.ToLower(args[0])
	if first == "off" {
		d.clearSticky(ctx, chatID)
		return
	}
	if first == "new" || first == "add" || first == "create" {
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		if name == "" {
			_, _ = d.opts.Client.SendMessage(ctx, chatID,
				"Usage: `/ledger new <name>` — for example `/ledger new Tokyo Trip`.",
				&SendMessageOpts{ParseMode: "Markdown"})
			return
		}
		d.startCreateConfirm(ctx, chatID, pcCreateLedger, name, 0, 0, true /* sticky */)
		return
	}

	// Set sticky to an existing ledger; offer create on miss.
	name := strings.TrimSpace(strings.Join(args, " "))
	leds, err := d.opts.Q.ListActiveLedgers(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load ledgers.", nil)
		return
	}
	if len(leds) > 0 {
		if pick := fuzzy.Best(name, ledgerNames(leds)); pick != "" {
			d.setSticky(ctx, chatID, pick, leds)
			return
		}
	}
	// No match — offer to create.
	d.startCreateConfirm(ctx, chatID, pcCreateLedger, name, 0, 0, true /* sticky */)
}

func (d *Dispatcher) clearSticky(ctx context.Context, chatID int64) {
	if err := d.opts.Q.SetTelegramActiveLedger(ctx, sqlcgen.SetTelegramActiveLedgerParams{
		ActiveLedgerID: sql.NullInt64{},
		UpdatedAt:      time.Now().UTC().Unix(),
	}); err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't clear active ledger.", nil)
		return
	}
	_ = d.opts.Client.UnpinAllChatMessages(ctx, chatID)
	_, _ = d.opts.Client.SendMessage(ctx, chatID, "📒 Active ledger cleared. Future expenses won't be auto-tagged.", nil)
}

func (d *Dispatcher) setSticky(ctx context.Context, chatID int64, ledgerName string, leds []sqlcgen.Ledger) {
	var ledger sqlcgen.Ledger
	for _, l := range leds {
		if l.Name == ledgerName {
			ledger = l
			break
		}
	}
	if ledger.ID == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't find that ledger.", nil)
		return
	}
	if err := d.opts.Q.SetTelegramActiveLedger(ctx, sqlcgen.SetTelegramActiveLedgerParams{
		ActiveLedgerID: sql.NullInt64{Int64: ledger.ID, Valid: true},
		UpdatedAt:      time.Now().UTC().Unix(),
	}); err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't set active ledger.", nil)
		return
	}
	body := fmt.Sprintf("📒 *Active ledger:* %s\n\nNew expenses will be tagged here until you `/ledger off`.",
		EscapeMarkdown(ledger.Name))
	sent, err := d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{ParseMode: "Markdown"})
	if err == nil && sent.MessageID > 0 {
		_ = d.opts.Client.UnpinAllChatMessages(ctx, chatID)
		_ = d.opts.Client.PinChatMessage(ctx, chatID, sent.MessageID)
	}
}

func (d *Dispatcher) cmdCategories(ctx context.Context, chatID int64) {
	cats, err := d.opts.Q.ListActiveCategories(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load categories.", nil)
		return
	}
	var b strings.Builder
	b.WriteString("🏷 *Your categories*\n\n")
	if len(cats) == 0 {
		b.WriteString("_None active. Add one with_ `/category new <name>`")
	} else {
		for _, c := range cats {
			b.WriteString(fmt.Sprintf("• %s\n", EscapeMarkdown(c.Name)))
		}
		b.WriteString("\nAdd new ones with `/category new <name>`.")
	}
	_, _ = d.opts.Client.SendMessage(ctx, chatID, b.String(), &SendMessageOpts{ParseMode: "Markdown"})
}

// cmdCategory only supports `/category new <name>` for creation. Categories
// don't have a sticky-context concept like ledgers do.
func (d *Dispatcher) cmdCategory(ctx context.Context, chatID int64, args []string) {
	if len(args) == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Usage: `/category new <name>` — for example `/category new Pets`. See `/categories` for the list.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	first := strings.ToLower(args[0])
	if first != "new" && first != "add" && first != "create" {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Categories don't have sticky context. To add one: `/category new <name>`.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	name := strings.TrimSpace(strings.Join(args[1:], " "))
	if name == "" {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Usage: `/category new <name>` — for example `/category new Pets`.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	d.startCreateConfirm(ctx, chatID, pcCreateCategory, name, 0, 0, false)
}

// cmdLedgerSummary backs the "ledger_summary" query intent: total spent in a
// specific ledger.
func (d *Dispatcher) cmdLedgerSummary(ctx context.Context, chatID int64, hint string) {
	owner, _ := d.opts.Q.GetOwner(ctx)
	leds, err := d.opts.Q.ListActiveLedgers(ctx)
	if err != nil || len(leds) == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "No ledgers exist yet.", nil)
		return
	}
	pick := fuzzy.Best(hint, ledgerNames(leds))
	if pick == "" {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			fmt.Sprintf("No ledger matching *%s*. Try `/ledgers`.", EscapeMarkdown(hint)),
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	var ledger sqlcgen.Ledger
	for _, l := range leds {
		if l.Name == pick {
			ledger = l
			break
		}
	}
	total, err := d.opts.Q.LedgerTotalSpent(ctx, sql.NullInt64{Int64: ledger.ID, Valid: true})
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't compute the ledger total.", nil)
		return
	}
	count, _ := d.opts.Q.LedgerExpenseCount(ctx, sql.NullInt64{Int64: ledger.ID, Valid: true})
	body := fmt.Sprintf("📒 *%s*\n\n%s — %d %s",
		EscapeMarkdown(ledger.Name),
		EscapeMarkdown(models.FormatMoney(total, owner.CurrencySymbol)),
		count, pluralExpense(count))
	_, _ = d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{ParseMode: "Markdown"})
}

// ─── Free text & query intent ──────────────────────────────────────────────

// handleFreeText is the single entry point for any non-command message after
// the LLM-purge refactor. One LLM call drives everything:
//
//   1. Send a placeholder ("📥 …") so the user sees an immediate reply.
//   2. Build the prompt context (owner, categories, ledgers, sticky ledger).
//   3. Call the unified LLM prompt — returns intent + expenses[]/query/reason.
//   4. Branch:
//        len(expenses) == 1  → single expense; edit placeholder to confirmation
//        len(expenses) >= 2  → batch insert; edit placeholder to summary + Undo
//        intent == "query"   → execute query; remove placeholder, send result
//        intent == "unclear" → edit placeholder to the model's one-line reason
//
// No regex pre-pass, no batch heuristic, no second classification round-trip.
func (d *Dispatcher) handleFreeText(ctx context.Context, m *Message, text string) {
	chatID := m.Chat.ID

	placeholder, err := d.opts.Client.SendMessage(ctx, chatID, "📥 Working on it…", nil)
	if err != nil {
		slog.Warn("telegram: placeholder send", "error", err)
		return
	}
	placeholderID := placeholder.MessageID

	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		d.editPlain(ctx, chatID, placeholderID, "_Couldn't load your profile._")
		return
	}
	llmCfg, engine, err := d.loadLLM(ctx)
	if err != nil {
		d.editPlain(ctx, chatID, placeholderID, llmSetupHint(err))
		return
	}

	cats, _ := d.opts.Q.ListActiveCategories(ctx)
	leds, _ := d.opts.Q.ListActiveLedgers(ctx)
	tg, _ := d.opts.Q.GetTelegramConfig(ctx)
	loc := models.LoadLocation(owner.Timezone)

	parse, err := d.callUnifiedLLM(ctx, engine, llmCfg.TextModel, owner, cats, leds, tg, text, nil)
	if err != nil {
		slog.Warn("telegram: LLM call failed", "error", err)
		d.editPlain(ctx, chatID, placeholderID, "_Couldn't reach the LLM right now. Please try again in a moment._")
		return
	}

	switch parse.Intent {
	case "expenses":
		if len(parse.Expenses) == 0 {
			d.editPlain(ctx, chatID, placeholderID, "_I couldn't pick out any expenses from that. Try something like_ `5000 fuel`.")
			return
		}
		// Low-confidence single-expense gate: prompt before committing.
		// Batch parses bypass this — the existing "↩ Undo all" pattern is
		// the escape hatch for batches.
		if len(parse.Expenses) == 1 && shouldPrompt(parse.Expenses[0]) {
			d.startPendingExpenseConfirm(ctx, chatID, placeholderID, parse.Expenses[0], owner, loc, false)
			return
		}
		logged, skipped := d.commitExpenses(ctx, parse.Expenses, owner, cats, leds, tg, loc)
		if len(logged) == 1 && len(skipped) == 0 {
			it := logged[0]
			body, kbd := d.renderConfirmation(ctx, it.ExpenseID, it.Amount, it.Description, owner,
				time.Unix(d.lookupSpentAt(ctx, it.ExpenseID), 0), loc,
				it.CategoryName, it.LedgerName, false)
			_ = d.opts.Client.EditMessageText(ctx, chatID, placeholderID, body, &SendMessageOpts{
				ParseMode:   "Markdown",
				ReplyMarkup: kbd,
			})
			return
		}
		body, kbd := d.renderBatchSummary(ctx, logged, skipped, owner)
		_ = d.opts.Client.EditMessageText(ctx, chatID, placeholderID, body, &SendMessageOpts{
			ParseMode:   "Markdown",
			ReplyMarkup: kbd,
		})

	case "query":
		// Drop the placeholder; the cmd handlers send their own message.
		_ = d.opts.Client.DeleteMessage(ctx, chatID, placeholderID)
		d.executeQuery(ctx, chatID, parse.Query)

	case "unclear":
		msg := strings.TrimSpace(parse.Reason)
		if msg == "" {
			msg = "I couldn't understand that — try something like `5000 fuel`, `12.50 coffee yesterday`, or `how much this month?`."
		}
		d.editPlain(ctx, chatID, placeholderID, msg)

	default:
		d.editPlain(ctx, chatID, placeholderID, "_I couldn't understand that._")
	}
}

// callUnifiedLLM builds the system prompt and runs one Engine.Complete call.
// Returns the parsed unified response. images is optional: when non-empty,
// the request is multimodal and the prompt's "receipt images" section
// becomes load-bearing.
func (d *Dispatcher) callUnifiedLLM(ctx context.Context, engine *llm.Engine, model string, owner sqlcgen.Owner, cats []sqlcgen.Category, leds []sqlcgen.Ledger, tg sqlcgen.TelegramConfig, userMsg string, images []llm.ImageInput) (llm.Parse, error) {
	var activeLedger string
	if tg.ActiveLedgerID.Valid {
		for _, l := range leds {
			if l.ID == tg.ActiveLedgerID.Int64 {
				activeLedger = l.Name
				break
			}
		}
	}
	loc := models.LoadLocation(owner.Timezone)
	systemPrompt, err := llm.SystemPrompt(llm.PromptContext{
		CurrencySymbol: owner.CurrencySymbol,
		CurrencyCode:   owner.CurrencyCode,
		Timezone:       owner.Timezone,
		NowRFC3339:     time.Now().In(loc).Format(time.RFC3339),
		ActiveLedger:   activeLedger,
		Categories:     categoryNames(cats),
		Ledgers:        ledgerNames(leds),
	})
	if err != nil {
		return llm.Parse{}, fmt.Errorf("build prompt: %w", err)
	}
	resp, err := engine.Complete(ctx, llm.PurposeParseExpense, llm.Request{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserMessage:  userMsg,
		Images:       images,
		JSONMode:     true,
		Temperature:  0,
		MaxTokens:    800, // generous — batches with 10+ items need room
	})
	if err != nil {
		return llm.Parse{}, err
	}
	parse, err := llm.ParseResponse(resp.Text)
	if err != nil {
		return llm.Parse{}, err
	}
	return parse, nil
}

// commitExpenses inserts every parseable item and returns logged + skipped
// slices for the renderer. Items with no amount or DB errors land in skipped.
func (d *Dispatcher) commitExpenses(ctx context.Context, items []llm.ExpenseItem, owner sqlcgen.Owner, cats []sqlcgen.Category, leds []sqlcgen.Ledger, tg sqlcgen.TelegramConfig, loc *time.Location) (logged []loggedExpense, skipped []skippedItem) {
	now := time.Now().UTC().Unix()
	for _, p := range items {
		desc := strings.TrimSpace(p.Description)
		if desc == "" {
			desc = "Expense"
		}
		if p.Amount == nil || *p.Amount <= 0 {
			skipped = append(skipped, skippedItem{Description: desc, Reason: "no amount"})
			continue
		}
		when := resolveSpentAt(p.SpentAt, loc)
		categoryID := matchCategory(p.CategoryHint, cats)
		ledgerID, ledgerName := resolveLedger(p.LedgerHint, leds, tg.ActiveLedgerID)

		id, err := d.opts.Q.CreateExpense(ctx, sqlcgen.CreateExpenseParams{
			Amount:      *p.Amount,
			Description: desc,
			Notes:       sql.NullString{},
			SpentAt:     when.Unix(),
			CategoryID:  categoryID,
			LedgerID:    ledgerID,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			slog.Warn("telegram: create expense", "error", err)
			skipped = append(skipped, skippedItem{Description: desc, Reason: "DB error"})
			continue
		}
		logged = append(logged, loggedExpense{
			ExpenseID:    id,
			Amount:       *p.Amount,
			Description:  desc,
			CategoryName: lookupCategoryName(categoryID, cats),
			LedgerName:   ledgerName,
		})
	}
	return logged, skipped
}

// executeQuery routes a parsed QueryIntent to the matching read handler.
// No second LLM call — the unified prompt already returned the structured
// query alongside the intent classification.
func (d *Dispatcher) executeQuery(ctx context.Context, chatID int64, q *llm.QueryIntent) {
	if q == nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Not sure what you meant. Try `/today`, `/month`, or `/help`.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	switch q.Intent {
	case "today":
		d.cmdSummary(ctx, chatID, "today")
	case "week":
		d.cmdSummary(ctx, chatID, "week")
	case "month":
		d.cmdSummary(ctx, chatID, "month")
	case "year":
		d.cmdSummary(ctx, chatID, "year")
	case "last_n":
		n := q.N
		if n <= 0 {
			n = 5
		}
		d.cmdLast(ctx, chatID, n)
	case "ledger_summary":
		d.cmdLedgerSummary(ctx, chatID, q.LedgerHint)
	default:
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"Not sure what you meant. Try `/today`, `/month`, or `/help`.",
			&SendMessageOpts{ParseMode: "Markdown"})
	}
}

// lookupSpentAt fetches just the spent_at timestamp for an expense — used
// by the single-confirmation render path so we don't pass it through a long
// arg list. Returns 0 on error (caller treats that as "now").
func (d *Dispatcher) lookupSpentAt(ctx context.Context, id int64) int64 {
	exp, err := d.opts.Q.GetExpense(ctx, id)
	if err != nil {
		return time.Now().UTC().Unix()
	}
	return exp.SpentAt
}

// ─── Confirmation + callbacks ──────────────────────────────────────────────

// renderConfirmation builds the body + inline keyboard for a confirmation.
// Used by both initial sends and post-callback in-place edits.
func (d *Dispatcher) renderConfirmation(ctx context.Context, expenseID int64, amount int64, desc string, owner sqlcgen.Owner, when time.Time, loc *time.Location, category, ledger string, edited bool) (string, *InlineKeyboard) {
	verb := "✅ Logged"
	if edited {
		verb = "✏️ Updated"
	}
	body := fmt.Sprintf("%s *%s* — %s\n📅 %s",
		verb,
		EscapeMarkdown(models.FormatMoney(amount, owner.CurrencySymbol)),
		EscapeMarkdown(desc),
		EscapeMarkdown(relativeDateLabel(when, loc)))
	if category != "" {
		body += " · 🏷 " + EscapeMarkdown(category)
	}
	if ledger != "" {
		body += " · 📒 " + EscapeMarkdown(ledger)
	}
	if total := d.todayTotal(ctx, loc); total > 0 {
		body += fmt.Sprintf("\n📊 _Today:_ %s",
			EscapeMarkdown(models.FormatMoney(total, owner.CurrencySymbol)))
	}

	idStr := strconv.FormatInt(expenseID, 10)
	rows := [][]InlineKeyboardButton{
		{
			{Text: "✏️ Edit", CallbackData: "edit:" + idStr},
			{Text: "🗑 Delete", CallbackData: "delete:" + idStr},
		},
		{
			{Text: "🏷 Category…", CallbackData: "cat:" + idStr},
			{Text: "📒 Ledger…", CallbackData: "ledger:" + idStr},
		},
	}
	if btn := dashboardURLButton(owner); btn != nil {
		rows = append(rows, btn)
	}
	return body, &InlineKeyboard{InlineKeyboard: rows}
}

// rerenderConfirmationFromDB pulls the current expense state and edits the
// message in place. Used after Category/Ledger picker resolutions.
func (d *Dispatcher) rerenderConfirmationFromDB(ctx context.Context, chatID, msgID, expenseID int64) {
	exp, err := d.opts.Q.GetExpense(ctx, expenseID)
	if err != nil {
		return
	}
	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		return
	}
	loc := models.LoadLocation(owner.Timezone)
	cats, _ := d.opts.Q.ListActiveCategories(ctx)
	leds, _ := d.opts.Q.ListActiveLedgers(ctx)
	categoryName := lookupCategoryName(exp.CategoryID, cats)
	ledgerName := lookupLedgerName(exp.LedgerID, leds)
	body, kbd := d.renderConfirmation(ctx, exp.ID, exp.Amount, exp.Description, owner,
		time.Unix(exp.SpentAt, 0), loc, categoryName, ledgerName, false)
	_ = d.opts.Client.EditMessageText(ctx, chatID, msgID, body, &SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kbd,
	})
}

// editConfirmation rewrites an existing confirmation message in place.
func (d *Dispatcher) editConfirmation(ctx context.Context, chatID, messageID int64, body string, kbd *InlineKeyboard) {
	_ = d.opts.Client.EditMessageText(ctx, chatID, messageID, body, &SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kbd,
	})
}

// editPlain edits a message with no keyboard and no markdown — used for
// transitioning picker/prompt messages through the create flow.
func (d *Dispatcher) editPlain(ctx context.Context, chatID, messageID int64, body string) {
	_ = d.opts.Client.EditMessageText(ctx, chatID, messageID, body, &SendMessageOpts{
		ParseMode: "Markdown",
	})
}

// handleCallback dispatches inline-keyboard taps. Callback data formats:
//
//	edit:<expense_id>
//	delete:<expense_id>
//	ledger:<expense_id>                       — opens the ledger picker
//	cat:<expense_id>                          — opens the category picker
//	lset:<expense_id>:<orig_msg_id>:<ledger_id>
//	cset:<expense_id>:<orig_msg_id>:<category_id>
//	lnew:<expense_id>:<orig_msg_id>           — start new-ledger flow from picker
//	cnew:<expense_id>:<orig_msg_id>           — start new-category flow from picker
//	mkconf:y / mkconf:n                       — Yes/Cancel for an open create-confirm
//	cmd:<command>[:<arg>]                     — quick-action from /help
func (d *Dispatcher) handleCallback(ctx context.Context, cq *CallbackQuery) {
	if cq == nil || cq.Message == nil {
		return
	}
	parts := strings.SplitN(cq.Data, ":", 4)
	if len(parts) == 0 {
		_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")
		return
	}
	switch parts[0] {
	case "edit":
		id, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		d.cbEdit(ctx, cq, id)
	case "delete":
		id, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		d.cbDelete(ctx, cq, id)
	case "ledger":
		id, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		d.cbOpenPicker(ctx, cq, id, pickerLedger)
	case "cat":
		id, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		d.cbOpenPicker(ctx, cq, id, pickerCategory)
	case "lset":
		expID, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		origMsg, _ := strconv.ParseInt(getOr(parts, 2, ""), 10, 64)
		ledID, _ := strconv.ParseInt(getOr(parts, 3, ""), 10, 64)
		d.cbAssignLedger(ctx, cq, expID, origMsg, ledID)
	case "cset":
		expID, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		origMsg, _ := strconv.ParseInt(getOr(parts, 2, ""), 10, 64)
		catID, _ := strconv.ParseInt(getOr(parts, 3, ""), 10, 64)
		d.cbAssignCategory(ctx, cq, expID, origMsg, catID)
	case "lnew":
		expID, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		origMsg, _ := strconv.ParseInt(getOr(parts, 2, ""), 10, 64)
		d.cbStartCreate(ctx, cq, fuNewLedgerName, expID, origMsg)
	case "cnew":
		expID, _ := strconv.ParseInt(getOr(parts, 1, ""), 10, 64)
		origMsg, _ := strconv.ParseInt(getOr(parts, 2, ""), 10, 64)
		d.cbStartCreate(ctx, cq, fuNewCategoryName, expID, origMsg)
	case "mkconf":
		d.cbConfirmCreate(ctx, cq, getOr(parts, 1, ""))
	case "pxe":
		// Pending eXpense (low-confidence prompt): y/e/n
		d.cbPendingExpense(ctx, cq, getOr(parts, 1, ""))
	case "undobatch":
		d.cbUndoBatch(ctx, cq, getOr(parts, 1, ""))
	case "cmd":
		d.cbCmd(ctx, cq, parts[1:])
	default:
		_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")
	}
}

// ─── Edit / Delete callbacks ───────────────────────────────────────────────

func (d *Dispatcher) cbEdit(ctx context.Context, cq *CallbackQuery, expenseID int64) {
	chatID := cq.Message.Chat.ID
	// Capture the confirmation's message_id so the reply handler can edit
	// the original confirmation in place (rather than send a duplicate).
	d.mu.Lock()
	d.followUps[chatID] = followUp{
		Kind:      fuEdit,
		ExpenseID: expenseID,
		OrigMsgID: cq.Message.MessageID,
	}
	d.mu.Unlock()
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")
	_, _ = d.opts.Client.SendMessage(ctx, chatID,
		fmt.Sprintf("Reply with the corrected amount, description, or full text. I'll update expense #%d. (Send `/cancel` to abort.)", expenseID),
		&SendMessageOpts{ParseMode: "Markdown"})
}

func (d *Dispatcher) cbDelete(ctx context.Context, cq *CallbackQuery, expenseID int64) {
	chatID := cq.Message.Chat.ID
	exp, err := d.opts.Q.GetExpense(ctx, expenseID)
	if err != nil {
		_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Already deleted.")
		d.editConfirmation(ctx, chatID, cq.Message.MessageID, "❌ _Already deleted._", nil)
		return
	}
	now := time.Now().UTC().Unix()
	if err := d.opts.Q.SoftDeleteExpense(ctx, sqlcgen.SoftDeleteExpenseParams{
		DeletedAt: sql.NullInt64{Int64: now, Valid: true},
		UpdatedAt: now,
		ID:        expenseID,
	}); err != nil {
		_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Couldn't delete.")
		return
	}
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Deleted")
	owner, _ := d.opts.Q.GetOwner(ctx)
	body := fmt.Sprintf("❌ *Deleted:* %s — %s",
		EscapeMarkdown(models.FormatMoney(exp.Amount, owner.CurrencySymbol)),
		EscapeMarkdown(exp.Description))
	d.editConfirmation(ctx, chatID, cq.Message.MessageID, body, nil)
}

// ─── Pickers (ledger + category) ───────────────────────────────────────────

type pickerKind int

const (
	pickerLedger pickerKind = iota + 1
	pickerCategory
)

const itemsPerPicker = 6

// cbOpenPicker sends a NEW message containing a picker for the requested kind.
// The original confirmation message_id is encoded into every picker button so
// the resolver can edit the original in place.
func (d *Dispatcher) cbOpenPicker(ctx context.Context, cq *CallbackQuery, expenseID int64, kind pickerKind) {
	chatID := cq.Message.Chat.ID
	origMsgID := cq.Message.MessageID

	var (
		title       string
		buttons     [][]InlineKeyboardButton
		noPrefix    string
		newLabel    string
		newCallback string
		setPrefix   string
	)
	switch kind {
	case pickerLedger:
		title = "Pick a ledger for this expense:"
		noPrefix = "lset"
		newLabel = "✨ Create new ledger…"
		newCallback = fmt.Sprintf("lnew:%d:%d", expenseID, origMsgID)
		setPrefix = "lset"
		leds, err := d.opts.Q.ListActiveLedgers(ctx)
		if err != nil {
			_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Couldn't load ledgers.")
			return
		}
		for _, l := range leds[:minInt(itemsPerPicker, len(leds))] {
			buttons = append(buttons, []InlineKeyboardButton{{
				Text:         "📒 " + l.Name,
				CallbackData: fmt.Sprintf("%s:%d:%d:%d", setPrefix, expenseID, origMsgID, l.ID),
			}})
		}
	case pickerCategory:
		title = "Pick a category for this expense:"
		noPrefix = "cset"
		newLabel = "✨ Create new category…"
		newCallback = fmt.Sprintf("cnew:%d:%d", expenseID, origMsgID)
		setPrefix = "cset"
		cats, err := d.opts.Q.ListActiveCategories(ctx)
		if err != nil {
			_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Couldn't load categories.")
			return
		}
		for _, c := range cats[:minInt(itemsPerPicker, len(cats))] {
			buttons = append(buttons, []InlineKeyboardButton{{
				Text:         "🏷 " + c.Name,
				CallbackData: fmt.Sprintf("%s:%d:%d:%d", setPrefix, expenseID, origMsgID, c.ID),
			}})
		}
	}

	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")
	buttons = append(buttons, []InlineKeyboardButton{{
		Text:         fmt.Sprintf("— No %s —", strings.ToLower(strings.TrimSuffix(strings.Split(title, " ")[1], "s"))),
		CallbackData: fmt.Sprintf("%s:%d:%d:0", noPrefix, expenseID, origMsgID),
	}})
	buttons = append(buttons, []InlineKeyboardButton{{
		Text:         newLabel,
		CallbackData: newCallback,
	}})

	_, _ = d.opts.Client.SendMessage(ctx, chatID, title,
		&SendMessageOpts{ReplyMarkup: &InlineKeyboard{InlineKeyboard: buttons}})
}

func (d *Dispatcher) cbAssignLedger(ctx context.Context, cq *CallbackQuery, expenseID, origMsgID, ledgerID int64) {
	chatID := cq.Message.Chat.ID
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")

	exp, err := d.opts.Q.GetExpense(ctx, expenseID)
	if err != nil {
		_ = d.opts.Client.DeleteMessage(ctx, chatID, cq.Message.MessageID)
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "That expense was deleted.", nil)
		return
	}
	var newLedger sql.NullInt64
	if ledgerID > 0 {
		newLedger = sql.NullInt64{Int64: ledgerID, Valid: true}
	}
	if err := d.opts.Q.UpdateExpense(ctx, sqlcgen.UpdateExpenseParams{
		Amount: exp.Amount, Description: exp.Description, Notes: exp.Notes,
		SpentAt: exp.SpentAt, CategoryID: exp.CategoryID, LedgerID: newLedger,
		UpdatedAt: time.Now().UTC().Unix(), ID: expenseID,
	}); err != nil {
		return
	}
	d.rerenderConfirmationFromDB(ctx, chatID, origMsgID, expenseID)
	_ = d.opts.Client.DeleteMessage(ctx, chatID, cq.Message.MessageID)
}

func (d *Dispatcher) cbAssignCategory(ctx context.Context, cq *CallbackQuery, expenseID, origMsgID, categoryID int64) {
	chatID := cq.Message.Chat.ID
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")

	exp, err := d.opts.Q.GetExpense(ctx, expenseID)
	if err != nil {
		_ = d.opts.Client.DeleteMessage(ctx, chatID, cq.Message.MessageID)
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "That expense was deleted.", nil)
		return
	}
	var newCat sql.NullInt64
	if categoryID > 0 {
		newCat = sql.NullInt64{Int64: categoryID, Valid: true}
	}
	if err := d.opts.Q.UpdateExpense(ctx, sqlcgen.UpdateExpenseParams{
		Amount: exp.Amount, Description: exp.Description, Notes: exp.Notes,
		SpentAt: exp.SpentAt, CategoryID: newCat, LedgerID: exp.LedgerID,
		UpdatedAt: time.Now().UTC().Unix(), ID: expenseID,
	}); err != nil {
		return
	}
	d.rerenderConfirmationFromDB(ctx, chatID, origMsgID, expenseID)
	_ = d.opts.Client.DeleteMessage(ctx, chatID, cq.Message.MessageID)
}

// ─── New-name flow (typed reply → confirm prompt → create) ─────────────────

// cbStartCreate is hit when "+ New ledger…" or "+ New category…" is tapped.
// We edit the picker message in place to "Send the new <kind> name." and stash
// a follow-up entry so the user's next text message is interpreted as the name.
func (d *Dispatcher) cbStartCreate(ctx context.Context, cq *CallbackQuery, kind followUpKind, expenseID, origMsgID int64) {
	chatID := cq.Message.Chat.ID
	pickerMsgID := cq.Message.MessageID
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")

	d.mu.Lock()
	d.followUps[chatID] = followUp{
		Kind:        kind,
		ExpenseID:   expenseID,
		OrigMsgID:   origMsgID,
		PromptMsgID: pickerMsgID,
	}
	d.mu.Unlock()

	label := "ledger"
	if kind == fuNewCategoryName {
		label = "category"
	}
	d.editPlain(ctx, chatID, pickerMsgID,
		fmt.Sprintf("Send the new %s name. (Send `/cancel` to abort.)", label))
}

// handleNewName runs when the user types a name in response to "+ New …".
// Validates the name, then transitions the prompt message into a Yes/Cancel
// confirm prompt and stores a pendingConfirm entry.
func (d *Dispatcher) handleNewName(ctx context.Context, m *Message, fu followUp, kind pendingConfirmKind) {
	chatID := m.Chat.ID
	name := strings.TrimSpace(m.Text)

	if err := validateName(name); err != nil {
		// Re-arm the follow-up so the user can try again.
		d.mu.Lock()
		d.followUps[chatID] = fu
		d.mu.Unlock()
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			fmt.Sprintf("%s — please send a different name, or `/cancel` to abort.", err.Error()),
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}

	d.startCreateConfirm(ctx, chatID, kind, name, fu.ExpenseID, fu.OrigMsgID, false /* sticky */)
	// The startCreateConfirm path manages its own prompt message; the picker
	// message is now stale, so clean it up.
	if fu.PromptMsgID > 0 {
		_ = d.opts.Client.DeleteMessage(ctx, chatID, fu.PromptMsgID)
	}
}

// startCreateConfirm sends the "Create new <kind> '<name>'? [Yes] [Cancel]"
// prompt and registers a pendingConfirm. Used from three entry points:
//
//   1. New-name flow (picker → typed name)
//   2. /ledger new <name> or /category new <name>
//   3. /ledger <name> miss (with sticky=true)
func (d *Dispatcher) startCreateConfirm(ctx context.Context, chatID int64, kind pendingConfirmKind, name string, expenseID, origMsgID int64, setSticky bool) {
	if err := validateName(name); err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, err.Error(), nil)
		return
	}
	label := "ledger"
	if kind == pcCreateCategory {
		label = "category"
	}
	body := fmt.Sprintf("Create new %s *%s*?", label, EscapeMarkdown(name))
	kbd := &InlineKeyboard{
		InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "✨ Yes, create it", CallbackData: "mkconf:y"},
			{Text: "✕ Cancel", CallbackData: "mkconf:n"},
		}},
	}
	sent, err := d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kbd,
	})
	if err != nil {
		return
	}

	d.mu.Lock()
	d.pendingConfirms[chatID] = pendingConfirm{
		Kind:        kind,
		Name:        name,
		ExpenseID:   expenseID,
		OrigMsgID:   origMsgID,
		PromptMsgID: sent.MessageID,
		SetSticky:   setSticky,
	}
	d.mu.Unlock()
}

// cbConfirmCreate handles Yes/Cancel taps on a create-confirm prompt.
func (d *Dispatcher) cbConfirmCreate(ctx context.Context, cq *CallbackQuery, choice string) {
	chatID := cq.Message.Chat.ID
	d.mu.Lock()
	pc, ok := d.pendingConfirms[chatID]
	if ok {
		delete(d.pendingConfirms, chatID)
	}
	d.mu.Unlock()

	if !ok {
		// Stale prompt (bot restarted, etc.). Just acknowledge.
		_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Already handled.")
		d.editPlain(ctx, chatID, cq.Message.MessageID, "_Already handled._")
		return
	}
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")

	if choice != "y" {
		d.editPlain(ctx, chatID, pc.PromptMsgID, "✕ _Cancelled._")
		return
	}

	// Do the create.
	switch pc.Kind {
	case pcCreateLedger:
		d.doCreateLedger(ctx, chatID, pc)
	case pcCreateCategory:
		d.doCreateCategory(ctx, chatID, pc)
	}
}

// doCreateLedger inserts the ledger, attaches it to an expense if context
// supplied one, and updates the prompt + (optionally) the original confirmation.
// Handles UNIQUE constraint by treating an existing same-named ledger as the
// happy path.
func (d *Dispatcher) doCreateLedger(ctx context.Context, chatID int64, pc pendingConfirm) {
	now := time.Now().UTC().Unix()
	id, err := d.opts.Q.CreateLedger(ctx, sqlcgen.CreateLedgerParams{
		Name:         pc.Name,
		Description:  sql.NullString{},
		BudgetAmount: sql.NullInt64{},
		StartDate:    sql.NullInt64{},
		EndDate:      sql.NullInt64{},
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		// UNIQUE collision → use the existing one.
		if isUniqueViolation(err) {
			leds, _ := d.opts.Q.ListActiveLedgers(ctx)
			for _, l := range leds {
				if strings.EqualFold(l.Name, pc.Name) {
					id = l.ID
					break
				}
			}
		}
		if id == 0 {
			d.editPlain(ctx, chatID, pc.PromptMsgID, fmt.Sprintf("⚠️ Couldn't create ledger: %v", err))
			return
		}
	}

	// Tie it to the expense if there is one.
	if pc.ExpenseID > 0 {
		if exp, err := d.opts.Q.GetExpense(ctx, pc.ExpenseID); err == nil {
			_ = d.opts.Q.UpdateExpense(ctx, sqlcgen.UpdateExpenseParams{
				Amount: exp.Amount, Description: exp.Description, Notes: exp.Notes,
				SpentAt: exp.SpentAt, CategoryID: exp.CategoryID,
				LedgerID:  sql.NullInt64{Int64: id, Valid: true},
				UpdatedAt: now, ID: pc.ExpenseID,
			})
			if pc.OrigMsgID > 0 {
				d.rerenderConfirmationFromDB(ctx, chatID, pc.OrigMsgID, pc.ExpenseID)
			}
		}
	}

	// Sticky context if requested.
	if pc.SetSticky {
		_ = d.opts.Q.SetTelegramActiveLedger(ctx, sqlcgen.SetTelegramActiveLedgerParams{
			ActiveLedgerID: sql.NullInt64{Int64: id, Valid: true},
			UpdatedAt:      now,
		})
	}

	suffix := ""
	if pc.ExpenseID > 0 {
		suffix = " and assigned to the expense above"
	} else if pc.SetSticky {
		suffix = " and set as your active ledger"
	}
	d.editPlain(ctx, chatID, pc.PromptMsgID,
		fmt.Sprintf("✨ Created ledger *%s*%s.", EscapeMarkdown(pc.Name), suffix))
}

// doCreateCategory mirrors doCreateLedger for the categories table.
func (d *Dispatcher) doCreateCategory(ctx context.Context, chatID int64, pc pendingConfirm) {
	now := time.Now().UTC().Unix()
	id, err := d.opts.Q.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
		Name:      pc.Name,
		Color:     "#6b7280",
		Icon:      sql.NullString{},
		CreatedAt: now,
	})
	if err != nil {
		if isUniqueViolation(err) {
			cats, _ := d.opts.Q.ListActiveCategories(ctx)
			for _, c := range cats {
				if strings.EqualFold(c.Name, pc.Name) {
					id = c.ID
					break
				}
			}
		}
		if id == 0 {
			d.editPlain(ctx, chatID, pc.PromptMsgID, fmt.Sprintf("⚠️ Couldn't create category: %v", err))
			return
		}
	}

	if pc.ExpenseID > 0 {
		if exp, err := d.opts.Q.GetExpense(ctx, pc.ExpenseID); err == nil {
			_ = d.opts.Q.UpdateExpense(ctx, sqlcgen.UpdateExpenseParams{
				Amount: exp.Amount, Description: exp.Description, Notes: exp.Notes,
				SpentAt:    exp.SpentAt,
				CategoryID: sql.NullInt64{Int64: id, Valid: true},
				LedgerID:   exp.LedgerID,
				UpdatedAt:  now, ID: pc.ExpenseID,
			})
			if pc.OrigMsgID > 0 {
				d.rerenderConfirmationFromDB(ctx, chatID, pc.OrigMsgID, pc.ExpenseID)
			}
		}
	}

	suffix := ""
	if pc.ExpenseID > 0 {
		suffix = " and assigned to the expense above"
	}
	d.editPlain(ctx, chatID, pc.PromptMsgID,
		fmt.Sprintf("✨ Created category *%s*%s.", EscapeMarkdown(pc.Name), suffix))
}

// cbCmd executes a slash command via an inline-keyboard button (used on /help).
func (d *Dispatcher) cbCmd(ctx context.Context, cq *CallbackQuery, args []string) {
	chatID := cq.Message.Chat.ID
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "today", "week", "month", "year":
		d.cmdSummary(ctx, chatID, args[0])
	case "last":
		n := 5
		if len(args) > 1 {
			if v, err := strconv.Atoi(args[1]); err == nil && v > 0 {
				n = v
			}
		}
		d.cmdLast(ctx, chatID, n)
	case "ledgers":
		d.cmdLedgers(ctx, chatID)
	case "categories":
		d.cmdCategories(ctx, chatID)
	}
}

// handleEditReply consumes the user's correction text after they tapped
// Edit. One LLM call, no regex pre-pass. The first item from the parsed
// expenses[] is merged onto the existing expense; the original confirmation
// is then re-rendered in place via fu.OrigMsgID rather than a duplicate
// being appended to the chat.
func (d *Dispatcher) handleEditReply(ctx context.Context, m *Message, fu followUp) {
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}
	existing, err := d.opts.Q.GetExpense(ctx, fu.ExpenseID)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't find that expense to edit (it may have been deleted).", nil)
		return
	}

	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't load your profile.", nil)
		return
	}
	llmCfg, engine, err := d.loadLLM(ctx)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, llmSetupHint(err), &SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	cats, _ := d.opts.Q.ListActiveCategories(ctx)
	leds, _ := d.opts.Q.ListActiveLedgers(ctx)
	tg, _ := d.opts.Q.GetTelegramConfig(ctx)
	loc := models.LoadLocation(owner.Timezone)

	parse, err := d.callUnifiedLLM(ctx, engine, llmCfg.TextModel, owner, cats, leds, tg, text, nil)
	if err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't reach the LLM right now.", nil)
		return
	}
	if parse.Intent != "expenses" || len(parse.Expenses) == 0 {
		_, _ = d.opts.Client.SendMessage(ctx, chatID,
			"I couldn't understand that as an edit. Tap *✏️ Edit* again to retry.",
			&SendMessageOpts{ParseMode: "Markdown"})
		return
	}
	// Edits are conceptually one-to-one; if the model returned a batch we
	// just take the first item and ignore the rest.
	p := parse.Expenses[0]

	// Merge: any field the user clearly specified overrides the existing
	// expense; otherwise we keep what was there.
	newAmount := existing.Amount
	if p.Amount != nil && *p.Amount > 0 {
		newAmount = *p.Amount
	}
	newDesc := existing.Description
	if p.Description != "" {
		newDesc = p.Description
	}
	newWhen := existing.SpentAt
	if p.SpentAt != "" {
		newWhen = resolveSpentAt(p.SpentAt, loc).Unix()
	}
	newCat := existing.CategoryID
	if p.CategoryHint != "" {
		newCat = matchCategory(p.CategoryHint, cats)
	}
	newLed := existing.LedgerID
	var newLedName string
	if p.LedgerHint != "" {
		nl, ln := resolveLedger(p.LedgerHint, leds, sql.NullInt64{})
		newLed, newLedName = nl, ln
	} else if newLed.Valid {
		newLedName = lookupLedgerName(newLed, leds)
	}

	now := time.Now().UTC().Unix()
	if err := d.opts.Q.UpdateExpense(ctx, sqlcgen.UpdateExpenseParams{
		Amount: newAmount, Description: newDesc, Notes: existing.Notes,
		SpentAt: newWhen, CategoryID: newCat, LedgerID: newLed,
		UpdatedAt: now, ID: fu.ExpenseID,
	}); err != nil {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, "Couldn't update the expense.", nil)
		return
	}

	categoryName := lookupCategoryName(newCat, cats)
	body, kbd := d.renderConfirmation(ctx, fu.ExpenseID, newAmount, newDesc, owner,
		time.Unix(newWhen, 0), loc, categoryName, newLedName, true /* edited */)
	if fu.OrigMsgID > 0 {
		// Edit the original confirmation in place — no duplicate "Updated"
		// message stacking up in the chat.
		d.editConfirmation(ctx, chatID, fu.OrigMsgID, body, kbd)
	} else {
		// Fallback for old followUps (pre-refactor) without OrigMsgID.
		_, _ = d.opts.Client.SendMessage(ctx, chatID, body, &SendMessageOpts{
			ParseMode:   "Markdown",
			ReplyMarkup: kbd,
		})
	}
}

// ─── Shared helpers ────────────────────────────────────────────────────────

type loadedLLMCfg = sqlcgen.LlmConfig

func (d *Dispatcher) loadLLM(ctx context.Context) (loadedLLMCfg, *llm.Engine, error) {
	cfg, err := d.opts.Q.GetLLMConfig(ctx)
	if err != nil {
		return cfg, nil, fmt.Errorf("no LLM config: %w", err)
	}
	var provider llm.Provider
	if d.opts.LLMProvider != nil {
		// Test-only seam: skip Factory + decrypt and use the injected
		// provider directly. cfg.TextModel still drives which model the
		// caller passes through, but the wire calls go to the mock.
		provider = d.opts.LLMProvider
	} else {
		apiKey, err := d.opts.Secrets.Open(cfg.ApiKeyEncrypted.String)
		if err != nil {
			return cfg, nil, fmt.Errorf("decrypt api key: %w", err)
		}
		provider, err = llm.Factory(cfg.Provider, apiKey, nil)
		if err != nil {
			return cfg, nil, err
		}
	}
	return cfg, &llm.Engine{
		Provider: provider,
		Logger:   &llm.DBLogger{Q: d.opts.Q},
		Timeout:  d.opts.LLMTimeout,
	}, nil
}

func (d *Dispatcher) todayTotal(ctx context.Context, loc *time.Location) int64 {
	now := time.Now().In(loc)
	from := startOfDay(now, loc)
	to := from.AddDate(0, 0, 1)
	total, err := d.opts.Q.TotalSpentBetween(ctx, sqlcgen.TotalSpentBetweenParams{
		SpentAt: from.Unix(), SpentAt_2: to.Unix(),
	})
	if err != nil {
		return 0
	}
	return total
}

func categoryNames(cs []sqlcgen.Category) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func ledgerNames(ls []sqlcgen.Ledger) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Name)
	}
	return out
}

func matchCategory(hint string, cats []sqlcgen.Category) sql.NullInt64 {
	if hint == "" || len(cats) == 0 {
		return matchCategory("Other", cats)
	}
	pick := fuzzy.Best(hint, categoryNames(cats))
	if pick == "" {
		if hint != "Other" {
			return matchCategory("Other", cats)
		}
		return sql.NullInt64{}
	}
	for _, c := range cats {
		if c.Name == pick {
			return sql.NullInt64{Int64: c.ID, Valid: true}
		}
	}
	return sql.NullInt64{}
}

func lookupCategoryName(id sql.NullInt64, cats []sqlcgen.Category) string {
	if !id.Valid {
		return ""
	}
	for _, c := range cats {
		if c.ID == id.Int64 {
			return c.Name
		}
	}
	return ""
}

func lookupLedgerName(id sql.NullInt64, leds []sqlcgen.Ledger) string {
	if !id.Valid {
		return ""
	}
	for _, l := range leds {
		if l.ID == id.Int64 {
			return l.Name
		}
	}
	return ""
}

func resolveLedger(hint string, leds []sqlcgen.Ledger, sticky sql.NullInt64) (sql.NullInt64, string) {
	if sticky.Valid {
		for _, l := range leds {
			if l.ID == sticky.Int64 {
				return sticky, l.Name
			}
		}
	}
	if hint == "" || len(leds) == 0 {
		return sql.NullInt64{}, ""
	}
	pick := fuzzy.Best(hint, ledgerNames(leds))
	if pick == "" {
		return sql.NullInt64{}, ""
	}
	for _, l := range leds {
		if l.Name == pick {
			return sql.NullInt64{Int64: l.ID, Valid: true}, l.Name
		}
	}
	return sql.NullInt64{}, ""
}

func resolveSpentAt(s string, loc *time.Location) time.Time {
	now := time.Now().In(loc)
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return now
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc)
	}
	switch s {
	case "now", "today", "this morning":
		return now
	case "yesterday", "last night":
		return now.AddDate(0, 0, -1)
	}
	if d, ok := weekdayFromName(s); ok {
		diff := int(now.Weekday()) - int(d)
		if diff < 0 {
			diff += 7
		}
		return now.AddDate(0, 0, -diff)
	}
	if strings.HasSuffix(s, "days ago") || strings.HasSuffix(s, "day ago") {
		fields := strings.Fields(s)
		if len(fields) >= 1 {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				return now.AddDate(0, 0, -n)
			}
		}
	}
	return now
}

func weekdayFromName(s string) (time.Weekday, bool) {
	switch strings.ToLower(s) {
	case "monday", "mon":
		return time.Monday, true
	case "tuesday", "tue":
		return time.Tuesday, true
	case "wednesday", "wed":
		return time.Wednesday, true
	case "thursday", "thu":
		return time.Thursday, true
	case "friday", "fri":
		return time.Friday, true
	case "saturday", "sat":
		return time.Saturday, true
	case "sunday", "sun":
		return time.Sunday, true
	}
	return 0, false
}

func relativeDateLabel(t time.Time, loc *time.Location) string {
	now := time.Now().In(loc)
	t = t.In(loc)
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return "Today"
	}
	yest := now.AddDate(0, 0, -1)
	if t.Year() == yest.Year() && t.Month() == yest.Month() && t.Day() == yest.Day() {
		return "Yesterday"
	}
	return t.Format("Mon, Jan 2")
}

func helpText() string {
	return strings.Join([]string{
		"*Pennywise bot — quick reference*",
		"",
		"Just type an expense in plain language:",
		"`5000 fuel`",
		"`12.50 coffee`",
		"`30 groceries yesterday`",
		"",
		"*Log a batch* — one expense per line:",
		"```",
		"15 lunch",
		"40 groceries",
		"8 coffee",
		"```",
		"",
		"Or ask a question:",
		"`how much this month?`",
		"`how much have I spent this week?`",
		"",
		"*Commands*",
		"`/today` `/week` `/month` `/year` — totals",
		"`/last [n]` — last N expenses (default 5, max 20)",
		"`/undo` — delete the most recent expense",
		"`/ledgers` — list ledgers · `/ledger <name>` set sticky · `/ledger off` clear",
		"`/ledger new <name>` — add a new ledger",
		"`/categories` — list categories · `/category new <name>` — add one",
		"`/cancel` — abort an in-progress action",
		"`/help` — this message",
	}, "\n")
}

func helpQuickActions() *InlineKeyboard {
	return &InlineKeyboard{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "📊 Today", CallbackData: "cmd:today"},
				{Text: "📅 This month", CallbackData: "cmd:month"},
			},
			{
				{Text: "📜 Last 5", CallbackData: "cmd:last:5"},
				{Text: "📒 Ledgers", CallbackData: "cmd:ledgers"},
			},
		},
	}
}

func pluralExpense(n int64) string {
	if n == 1 {
		return "expense"
	}
	return "expenses"
}

func llmSetupHint(err error) string {
	_ = err
	return "LLM provider isn't set up or is failing. Configure it in *Pennywise → Settings → LLM Provider*."
}

func getOr(s []string, i int, dflt string) string {
	if i < 0 || i >= len(s) {
		return dflt
	}
	return s[i]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// validateName enforces the rules a ledger or category name must satisfy.
//
// Constraints:
//   - non-empty after trim
//   - max 80 chars (well under the 120-char schema limit, leaves headroom)
//   - must not start with "/" (looks like a slash command)
//   - must not contain newlines
func validateName(name string) error {
	if name == "" {
		return errors.New("Name can't be empty")
	}
	if strings.HasPrefix(name, "/") {
		return errors.New("Name can't start with `/`")
	}
	if strings.ContainsAny(name, "\n\r") {
		return errors.New("Name can't contain line breaks")
	}
	if len([]rune(name)) > 80 {
		return errors.New("Name too long (max 80 characters)")
	}
	return nil
}

// isUniqueViolation detects a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
