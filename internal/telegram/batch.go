package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/models"
)

// ─── Batch summary + undo ──────────────────────────────────────────────────
//
// Batch detection is now driven by the LLM: when the unified prompt returns
// `expenses[]` of length 2+, the dispatcher renders a single summary message
// instead of the per-expense confirmation. This file owns the summary
// rendering + the [↩ Undo all] callback flow.

// loggedExpense is the dispatcher's record of one successfully-inserted
// expense within a batch insert. The summary renderer uses this directly.
type loggedExpense struct {
	ExpenseID    int64
	Amount       int64
	Description  string
	CategoryName string
	LedgerName   string
}

// skippedItem captures one expense the LLM returned that we couldn't insert
// (no amount, DB error, etc.) along with a one-phrase reason for the user.
type skippedItem struct {
	Description string // best-guess label from the LLM, falling back to "(no description)"
	Reason      string
}

// maxBatchSummaryItems caps how many lines we render per kind so a 200-line
// batch can't overflow Telegram's 4096-char message limit.
const maxBatchSummaryItems = 15

// renderBatchSummary builds the summary body + the [↩ Undo all] keyboard for
// a multi-expense insert. logged are the rows that landed; skipped are the
// items the LLM proposed but we couldn't commit.
func (d *Dispatcher) renderBatchSummary(ctx context.Context, logged []loggedExpense, skipped []skippedItem, owner sqlcgen.Owner) (string, *InlineKeyboard) {
	loc := models.LoadLocation(owner.Timezone)

	var totalAmount int64
	loggedIDs := make([]int64, 0, len(logged))
	for _, it := range logged {
		totalAmount += it.Amount
		loggedIDs = append(loggedIDs, it.ExpenseID)
	}

	var b strings.Builder

	// All-skipped path: nothing landed, explain what went wrong.
	if len(logged) == 0 {
		b.WriteString("⚠️ Couldn't log any of those.\n\n")
		shown := 0
		for _, s := range skipped {
			if shown >= maxBatchSummaryItems {
				b.WriteString(fmt.Sprintf("• … and %d more\n", len(skipped)-shown))
				break
			}
			label := s.Description
			if label == "" {
				label = "(no description)"
			}
			label = truncateRunes(label, 60)
			if s.Reason != "" {
				b.WriteString(fmt.Sprintf("• %s — _%s_\n",
					EscapeMarkdown(label), EscapeMarkdown(s.Reason)))
			} else {
				b.WriteString(fmt.Sprintf("• %s\n", EscapeMarkdown(label)))
			}
			shown++
		}
		b.WriteString("\nTry again with clearer entries — for example one expense per line.")
		return b.String(), nil
	}

	// Header.
	if len(skipped) == 0 {
		b.WriteString(fmt.Sprintf("✅ Logged %d %s (*%s*)\n\n",
			len(logged), pluralExpense(int64(len(logged))),
			EscapeMarkdown(models.FormatMoney(totalAmount, owner.CurrencySymbol))))
	} else {
		b.WriteString(fmt.Sprintf("✅ Logged %d %s (*%s*) · ⚠️ %d skipped\n\n",
			len(logged), pluralExpense(int64(len(logged))),
			EscapeMarkdown(models.FormatMoney(totalAmount, owner.CurrencySymbol)),
			len(skipped)))
	}

	// Logged items (capped).
	shown := 0
	for _, it := range logged {
		if shown >= maxBatchSummaryItems {
			b.WriteString(fmt.Sprintf("• … and %d more\n", len(logged)-shown))
			break
		}
		line := fmt.Sprintf("• *%s* — %s",
			EscapeMarkdown(models.FormatMoney(it.Amount, owner.CurrencySymbol)),
			EscapeMarkdown(it.Description))
		if it.CategoryName != "" {
			line += " · 🏷 " + EscapeMarkdown(it.CategoryName)
		}
		if it.LedgerName != "" {
			line += " · 📒 " + EscapeMarkdown(it.LedgerName)
		}
		b.WriteString(line + "\n")
		shown++
	}

	// Skipped section.
	if len(skipped) > 0 {
		b.WriteString("\n⚠️ *Skipped:*\n")
		shownSkipped := 0
		for _, s := range skipped {
			if shownSkipped >= maxBatchSummaryItems {
				b.WriteString(fmt.Sprintf("• … and %d more\n", len(skipped)-shownSkipped))
				break
			}
			label := s.Description
			if label == "" {
				label = "(no description)"
			}
			label = truncateRunes(label, 60)
			if s.Reason != "" {
				b.WriteString(fmt.Sprintf("• %s — _%s_\n",
					EscapeMarkdown(label), EscapeMarkdown(s.Reason)))
			} else {
				b.WriteString(fmt.Sprintf("• %s\n", EscapeMarkdown(label)))
			}
			shownSkipped++
		}
	}

	if total := d.todayTotal(ctx, loc); total > 0 {
		b.WriteString(fmt.Sprintf("\n📊 _Today:_ %s",
			EscapeMarkdown(models.FormatMoney(total, owner.CurrencySymbol))))
	}

	var kbd *InlineKeyboard
	if len(loggedIDs) > 0 {
		token := d.registerBatchUndo(loggedIDs)
		kbd = &InlineKeyboard{
			InlineKeyboard: [][]InlineKeyboardButton{{
				{
					Text:         fmt.Sprintf("↩ Undo all %d", len(logged)),
					CallbackData: "undobatch:" + token,
				},
			}},
		}
	}
	return b.String(), kbd
}

// registerBatchUndo stashes the IDs and returns a short token to embed in the
// callback. Tokens are monotonic base-36 strings (~5–7 chars typical), well
// under Telegram's 64-byte callback_data limit.
//
// We don't TTL the map; tokens accumulate for the bot's lifetime. Per chat
// the user typically issues a few batches a day so memory pressure is nil.
// Bot restart clears everything, which is acceptable — old [↩ Undo all]
// buttons just become inert.
func (d *Dispatcher) registerBatchUndo(ids []int64) string {
	token := strconv.FormatInt(d.batchTokenSeq.Add(1), 36)
	d.batchUndosMu.Lock()
	d.batchUndos[token] = ids
	d.batchUndosMu.Unlock()
	return token
}

// cbUndoBatch deletes every expense in the batch identified by token.
func (d *Dispatcher) cbUndoBatch(ctx context.Context, cq *CallbackQuery, token string) {
	chatID := cq.Message.Chat.ID

	d.batchUndosMu.Lock()
	ids, ok := d.batchUndos[token]
	if ok {
		delete(d.batchUndos, token)
	}
	d.batchUndosMu.Unlock()

	if !ok || len(ids) == 0 {
		_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, "Already undone.")
		d.editPlain(ctx, chatID, cq.Message.MessageID, "↩️ _Already undone._")
		return
	}

	now := time.Now().UTC().Unix()
	deleted := 0
	for _, id := range ids {
		if err := d.opts.Q.SoftDeleteExpense(ctx, sqlcgen.SoftDeleteExpenseParams{
			DeletedAt: sql.NullInt64{Int64: now, Valid: true},
			UpdatedAt: now,
			ID:        id,
		}); err == nil {
			deleted++
		}
	}
	_ = d.opts.Client.AnswerCallbackQuery(ctx, cq.ID, fmt.Sprintf("Undone %d", deleted))
	d.editPlain(ctx, chatID, cq.Message.MessageID,
		fmt.Sprintf("↩️ _Undone — removed %d expense%s._", deleted, pluralIfMany(deleted)))
}

func pluralIfMany(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncateRunes limits a string to n runes, appending "…" if it had to cut.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
