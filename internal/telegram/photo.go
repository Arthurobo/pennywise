package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/models"
)

// supportedImageMIMETypes is the set of inbound attachment MIME types we
// pass through to the LLM. Anything outside this set is rejected with a
// clear message rather than silently routed to the model — every vendor
// rejects unsupported types differently and the user gets confusing errors.
//
// Photos sent through the photo picker arrive as image/jpeg (Telegram
// re-encodes). PDFs and HEIC arrive as documents with the listed MIME.
var supportedImageMIMETypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
	"image/heic":      true,
	"image/heif":      true,
	"application/pdf": true,
}

// handlePhoto is the entry point for any inbound message that carries an
// image (Photo array) or an image-typed Document. The flow mirrors
// handleFreeText: send a placeholder, run one unified-LLM call (now with
// the image attached), and reuse commitExpenses + renderConfirmation so
// the confirmation UI is identical to the text path.
func (d *Dispatcher) handlePhoto(ctx context.Context, m *Message) {
	chatID := m.Chat.ID

	fileID, mime, errMsg := pickImageAttachment(m)
	if errMsg != "" {
		_, _ = d.opts.Client.SendMessage(ctx, chatID, errMsg, nil)
		return
	}

	placeholder, err := d.opts.Client.SendMessage(ctx, chatID, "📷 Reading your receipt…", nil)
	if err != nil {
		slog.Warn("telegram: photo placeholder send", "error", err)
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

	if !llm.ModelSupportsVision(llmCfg.Provider, llmCfg.TextModel) {
		d.editPlain(ctx, chatID, placeholderID, visionUnsupportedHint(llmCfg.Provider))
		return
	}

	// Per-provider MIME narrowing. xAI accepts JPG/PNG only; Anthropic
	// drops PDF/HEIC; Gemini accepts the widest set including PDF. The
	// global supportedImageMIMETypes upstream is just the outer envelope.
	if accepted := llm.ProviderImageMIMETypes(llmCfg.Provider); accepted != nil && !accepted[mime] {
		d.editPlain(ctx, chatID, placeholderID, providerMIMEMismatchHint(llmCfg.Provider, mime))
		return
	}

	file, err := d.opts.Client.GetFile(ctx, fileID)
	if err != nil {
		slog.Warn("telegram: getFile", "error", err)
		d.editPlain(ctx, chatID, placeholderID, "_Couldn't fetch the file from Telegram. Please try again._")
		return
	}
	data, err := d.opts.Client.DownloadFile(ctx, file.FilePath, DefaultMaxAttachmentBytes)
	if err != nil {
		if errors.Is(err, ErrFileTooLarge) {
			d.editPlain(ctx, chatID, placeholderID, "_That image is too large \\(max 10 MB\\). Please send a smaller copy._")
			return
		}
		slog.Warn("telegram: download attachment", "error", err)
		d.editPlain(ctx, chatID, placeholderID, "_Couldn't download the image from Telegram. Please try again._")
		return
	}

	cats, _ := d.opts.Q.ListActiveCategories(ctx)
	leds, _ := d.opts.Q.ListActiveLedgers(ctx)
	tg, _ := d.opts.Q.GetTelegramConfig(ctx)
	loc := models.LoadLocation(owner.Timezone)

	caption := strings.TrimSpace(m.Caption)
	parse, err := d.callUnifiedLLM(ctx, engine, llmCfg.TextModel, owner, cats, leds, tg, caption,
		[]llm.ImageInput{{MIMEType: mime, Data: data}})
	if err != nil {
		if errors.Is(err, llm.ErrVisionUnsupported) {
			// Defense in depth — catalog said vision-capable but the
			// provider disagreed. Surface the same hint as the catalog gate.
			d.editPlain(ctx, chatID, placeholderID, visionUnsupportedHint(llmCfg.Provider))
			return
		}
		slog.Warn("telegram: vision LLM call", "error", err)
		d.editPlain(ctx, chatID, placeholderID, "_Couldn't reach the LLM right now. Please try again in a moment._")
		return
	}

	switch parse.Intent {
	case "expenses":
		if len(parse.Expenses) == 0 {
			d.editPlain(ctx, chatID, placeholderID, "_I couldn't read expense details from that image. Try a clearer photo, or type the expense as text._")
			return
		}
		// Low-confidence gate (same as text path). Receipts can be ambiguous
		// too — faded ink, multiple totals, partial captures.
		if len(parse.Expenses) == 1 && shouldPrompt(parse.Expenses[0]) {
			d.startPendingExpenseConfirm(ctx, chatID, placeholderID, parse.Expenses[0], owner, loc, false)
			return
		}
		// One-image-one-expense by prompt design. If the model returns more,
		// we still commit them all — the prompt is the contract, this is a
		// safety net rather than a feature.
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

	case "unclear":
		msg := strings.TrimSpace(parse.Reason)
		if msg == "" {
			msg = "I couldn't read expense details from that image. Try a clearer photo of the receipt."
		}
		d.editPlain(ctx, chatID, placeholderID, "_"+EscapeMarkdown(msg)+"_")

	default:
		// Receipt images shouldn't yield query intent. Treat as unclear.
		d.editPlain(ctx, chatID, placeholderID, "_I couldn't read expense details from that image. Try a clearer photo of the receipt._")
	}
}

// pickImageAttachment extracts the (file_id, mime) pair from an inbound
// message and returns "" / errMsg when the attachment isn't usable. Photos
// take precedence over Documents when both are present (they shouldn't be,
// but defensive code is cheap).
func pickImageAttachment(m *Message) (fileID, mime, userErr string) {
	if len(m.Photo) > 0 {
		// Telegram sorts smallest → largest; the last entry has the most
		// detail, which matters when the receipt has small print.
		largest := m.Photo[len(m.Photo)-1]
		if largest.FileID == "" {
			return "", "", "Telegram sent a photo with no file ID. Please try again."
		}
		return largest.FileID, "image/jpeg", ""
	}
	if m.Document != nil && m.Document.FileID != "" {
		mt := strings.ToLower(strings.TrimSpace(m.Document.MIMEType))
		if mt == "" {
			return "", "", "I can only read receipt images and PDFs."
		}
		if !supportedImageMIMETypes[mt] {
			return "", "", "Unsupported file type. Send a JPG, PNG, WebP, HEIC, or PDF receipt."
		}
		return m.Document.FileID, mt, ""
	}
	return "", "", "I didn't see an image attached."
}

// isReceiptDocument reports whether a Telegram Document attachment has a
// MIME type we route through the photo handler. Used by the dispatcher's
// top-level routing to decide between "image flow" and "ignore".
func isReceiptDocument(d *Document) bool {
	if d == nil {
		return false
	}
	mt := strings.ToLower(strings.TrimSpace(d.MIMEType))
	return supportedImageMIMETypes[mt]
}

// visionUnsupportedHint returns the user-facing guidance shown when the
// current LLM model can't process images. We name the alternatives so the
// user knows exactly what to switch to in Settings → LLM.
func visionUnsupportedHint(currentProvider string) string {
	return "_Image uploads need a vision-capable model. Your current provider \\(*" +
		EscapeMarkdown(currentProvider) +
		"*\\) can't read images on the cheapest tier. Switch to a vision-capable provider in_ *Pennywise → Settings → LLM Provider*."
}

// providerMIMEMismatchHint explains that the current provider doesn't accept
// the user's image format. Names the accepted formats so the user can either
// re-export or switch providers.
func providerMIMEMismatchHint(provider, gotMIME string) string {
	accepted := "JPG, PNG"
	switch provider {
	case "openai", "anthropic":
		accepted = "JPG, PNG, WebP, or GIF"
	case "gemini":
		accepted = "JPG, PNG, WebP, HEIC, or PDF"
	case "xai":
		accepted = "JPG or PNG"
	}
	return "_Your provider \\(*" + EscapeMarkdown(provider) +
		"*\\) doesn't accept *" + EscapeMarkdown(gotMIME) +
		"*. Send a " + EscapeMarkdown(accepted) +
		" copy, or switch providers in_ *Pennywise → Settings → LLM Provider*."
}
