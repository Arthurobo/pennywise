package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/models"
)

// LowConfidenceThreshold is the cutoff below which a parsed expense is
// shown as a "Log this?" prompt instead of being silently committed. Sits
// at the top of the prompt's "0.6–0.9 inferred" band so we don't pester
// the user about minor hedging (0.8x) but reliably catch genuine ambiguity.
const LowConfidenceThreshold = 0.7

// pendingExpense holds a parsed-but-not-yet-committed expense that's
// awaiting Yes/Edit/Cancel. Keyed by chat ID in d.pendingExpenses.
//
// The whole ExpenseItem is captured (not just the amount + description)
// so the Yes branch can resolve category/ledger hints against the same
// canonical inputs as the original parse, even if the user has since
// added a category or ledger.
type pendingExpense struct {
	Item        llm.ExpenseItem
	PromptMsgID int64 // the confirmation message we edit on Yes/Edit/Cancel
	HasRetried  bool  // set true after one fuPendingEdit cycle to cap re-prompt loops
}

// shouldPrompt reports whether a parsed item warrants a pre-commit prompt.
// Returns false when Confidence is exactly 0 — that's a "no signal" sentinel
// (provider didn't populate the field), not low confidence. Otherwise true
// when below the threshold.
func shouldPrompt(item llm.ExpenseItem) bool {
	return item.Confidence > 0 && item.Confidence < LowConfidenceThreshold
}

// renderPendingPrompt builds the message + inline keyboard for a
// low-confidence pre-commit prompt. Format mirrors renderConfirmation
// but the verb is "❓ Confirm" and the buttons are Yes/Edit/Cancel.
func (d *Dispatcher) renderPendingPrompt(ctx context.Context, item llm.ExpenseItem, owner sqlcgen.Owner, loc *time.Location) (string, *InlineKeyboard) {
	amountStr := "(no amount)"
	if item.Amount != nil {
		amountStr = models.FormatMoney(*item.Amount, owner.CurrencySymbol)
	}
	desc := strings.TrimSpace(item.Description)
	if desc == "" {
		desc = "Expense"
	}
	when := resolveSpentAt(item.SpentAt, loc)
	body := fmt.Sprintf("❓ *Log this?* I'm not 100%% sure I read this right.\n\n*%s* — %s\n📅 %s",
		EscapeMarkdown(amountStr),
		EscapeMarkdown(desc),
		EscapeMarkdown(relativeDateLabel(when, loc)),
	)
	if h := strings.TrimSpace(item.CategoryHint); h != "" {
		body += " · 🏷 " + EscapeMarkdown(h)
	}
	if h := strings.TrimSpace(item.LedgerHint); h != "" {
		body += " · 📒 " + EscapeMarkdown(h)
	}
	body += fmt.Sprintf("\n_Confidence: %.0f%%_", item.Confidence*100)
	_ = ctx

	kbd := &InlineKeyboard{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "✓ Yes, log it", CallbackData: "pxe:y"},
			{Text: "✏️ Edit", CallbackData: "pxe:e"},
			{Text: "✕ Cancel", CallbackData: "pxe:n"},
		},
	}}
	return body, kbd
}

// startPendingExpenseConfirm edits the placeholder into the prompt and
// stashes pending state. Replaces commitExpenses + renderConfirmation for
// low-confidence single-expense parses.
func (d *Dispatcher) startPendingExpenseConfirm(ctx context.Context, chatID, placeholderID int64, item llm.ExpenseItem, owner sqlcgen.Owner, loc *time.Location, hasRetried bool) {
	body, kbd := d.renderPendingPrompt(ctx, item, owner, loc)
	if err := d.opts.Client.EditMessageText(ctx, chatID, placeholderID, body, &SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kbd,
	}); err != nil {
		slog.Warn("telegram: pending prompt edit", "error", err)
	}
	d.mu.Lock()
	d.pendingExpenses[chatID] = pendingExpense{
		Item:        item,
		PromptMsgID: placeholderID,
		HasRetried:  hasRetried,
	}
	d.mu.Unlock()
}

// cbPendingExpense handles Yes/Edit/Cancel taps on a low-confidence prompt.
// Callback data shape: "pxe:y" | "pxe:e" | "pxe:n".
func (d *Dispatcher) cbPendingExpense(ctx context.Context, cq *CallbackQuery, choice string) {
	if cq == nil || cq.Message == nil {
		return
	}
	chatID := cq.Message.Chat.ID

	d.mu.Lock()
	pe, ok := d.pendingExpenses[chatID]
	if ok {
		// On Yes/Cancel we consume the state immediately. On Edit we
		// keep it (the follow-up handler will read + clear it).
		if choice != "e" {
			delete(d.pendingExpenses, chatID)
		}
	}
	d.mu.Unlock()

	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "")

	if !ok {
		// Stale tap (e.g. user double-clicked, or pending state expired).
		d.editPlain(ctx, chatID, cq.Message.MessageID, "_This prompt is no longer active._")
		return
	}

	switch choice {
	case "y":
		d.confirmPendingExpense(ctx, chatID, pe)
	case "e":
		d.startPendingEdit(ctx, chatID, pe)
	case "n":
		d.editPlain(ctx, chatID, pe.PromptMsgID, "❌ _Discarded._")
	}
}

// confirmPendingExpense commits the stored item and edits the prompt into
// a normal "✅ Logged" confirmation with the standard action keyboard.
func (d *Dispatcher) confirmPendingExpense(ctx context.Context, chatID int64, pe pendingExpense) {
	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		d.editPlain(ctx, chatID, pe.PromptMsgID, "_Couldn't load your profile._")
		return
	}
	cats, _ := d.opts.Q.ListActiveCategories(ctx)
	leds, _ := d.opts.Q.ListActiveLedgers(ctx)
	tg, _ := d.opts.Q.GetTelegramConfig(ctx)
	loc := models.LoadLocation(owner.Timezone)

	logged, skipped := d.commitExpenses(ctx, []llm.ExpenseItem{pe.Item}, owner, cats, leds, tg, loc)
	if len(logged) != 1 || len(skipped) > 0 {
		d.editPlain(ctx, chatID, pe.PromptMsgID, "_Couldn't log that — please try typing it again._")
		return
	}
	it := logged[0]
	body, kbd := d.renderConfirmation(ctx, it.ExpenseID, it.Amount, it.Description, owner,
		time.Unix(d.lookupSpentAt(ctx, it.ExpenseID), 0), loc,
		it.CategoryName, it.LedgerName, false)
	d.editConfirmation(ctx, chatID, pe.PromptMsgID, body, kbd)
}

// startPendingEdit transitions the prompt into "type a correction" mode.
// Registers a fuPendingEdit follow-up so the next text message routes to
// handlePendingEditReply.
func (d *Dispatcher) startPendingEdit(ctx context.Context, chatID int64, pe pendingExpense) {
	d.mu.Lock()
	d.followUps[chatID] = followUp{
		Kind:        fuPendingEdit,
		ExpenseID:   0, // not yet in DB
		OrigMsgID:   pe.PromptMsgID,
		PromptMsgID: pe.PromptMsgID,
	}
	d.mu.Unlock()

	d.editPlain(ctx, chatID, pe.PromptMsgID,
		"✏️ _Type the corrected expense — for example, the full amount and what it was for._")
}

// handlePendingEditReply handles the user's free-text correction after a
// pxe:e tap. Re-parses through the LLM. If the new parse is still low
// confidence, we fall back to auto-commit (capped at one re-prompt) to
// avoid ping-pong.
func (d *Dispatcher) handlePendingEditReply(ctx context.Context, m *Message, fu followUp) {
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}

	d.mu.Lock()
	pe, hadPending := d.pendingExpenses[chatID]
	if hadPending {
		delete(d.pendingExpenses, chatID)
	}
	d.mu.Unlock()
	hasRetried := hadPending && pe.HasRetried

	owner, err := d.opts.Q.GetOwner(ctx)
	if err != nil {
		d.editPlain(ctx, chatID, fu.PromptMsgID, "_Couldn't load your profile._")
		return
	}
	llmCfg, engine, err := d.loadLLM(ctx)
	if err != nil {
		d.editPlain(ctx, chatID, fu.PromptMsgID, llmSetupHint(err))
		return
	}
	cats, _ := d.opts.Q.ListActiveCategories(ctx)
	leds, _ := d.opts.Q.ListActiveLedgers(ctx)
	tg, _ := d.opts.Q.GetTelegramConfig(ctx)
	loc := models.LoadLocation(owner.Timezone)

	parse, err := d.callUnifiedLLM(ctx, engine, llmCfg.TextModel, owner, cats, leds, tg, text, nil)
	if err != nil {
		slog.Warn("telegram: pending edit LLM call", "error", err)
		d.editPlain(ctx, chatID, fu.PromptMsgID, "_Couldn't reach the LLM right now. Please try again in a moment._")
		return
	}
	if parse.Intent != "expenses" || len(parse.Expenses) == 0 {
		msg := strings.TrimSpace(parse.Reason)
		if msg == "" {
			msg = "I couldn't pick out an expense from that. Try `12 coffee` or similar."
		}
		d.editPlain(ctx, chatID, fu.PromptMsgID, "_"+EscapeMarkdown(msg)+"_")
		return
	}

	// Take the first item (the user's correction targets a single expense).
	item := parse.Expenses[0]

	// If still low confidence and we haven't retried yet, prompt once more.
	// Otherwise commit and let the user adjust via the standard Edit button.
	if shouldPrompt(item) && !hasRetried {
		d.startPendingExpenseConfirm(ctx, chatID, fu.PromptMsgID, item, owner, loc, true)
		return
	}

	logged, skipped := d.commitExpenses(ctx, []llm.ExpenseItem{item}, owner, cats, leds, tg, loc)
	if len(logged) != 1 || len(skipped) > 0 {
		d.editPlain(ctx, chatID, fu.PromptMsgID, "_Couldn't log that — please try typing it again._")
		return
	}
	it := logged[0]
	body, kbd := d.renderConfirmation(ctx, it.ExpenseID, it.Amount, it.Description, owner,
		time.Unix(d.lookupSpentAt(ctx, it.ExpenseID), 0), loc,
		it.CategoryName, it.LedgerName, false)
	d.editConfirmation(ctx, chatID, fu.PromptMsgID, body, kbd)
}
