package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/fuzzy"
	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/models"
)

// maxReceiptBytes caps the inbound multipart body. The actual file is
// capped at the same value (10 MiB) — the +1 MiB on the parser limit
// is multipart-overhead headroom, not extra payload allowance.
const maxReceiptBytes = 10 * 1024 * 1024

// parseReceiptResponse is the JSON shape returned to the dashboard JS.
// On success, the populated fields are slotted directly into the form
// inputs by the client. On failure, the message is rendered inline and
// the form fields are left untouched.
type parseReceiptResponse struct {
	OK         bool    `json:"ok"`
	Message    string  `json:"message,omitempty"`
	AmountStr  string  `json:"amount_str,omitempty"`  // formatted for the amount input (decimal)
	Description string `json:"description,omitempty"`
	SpentAt    string  `json:"spent_at,omitempty"` // YYYY-MM-DD in owner's timezone
	CategoryID string  `json:"category_id,omitempty"`
	LedgerID   string  `json:"ledger_id,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// ParseReceipt POST /expenses/parse-receipt
//
// Accepts a multipart upload with field name "receipt", runs it through
// the configured vision-capable LLM, and returns parsed expense fields
// for the dashboard JS to slot into the New Expense form. The form is
// not auto-submitted — the user reviews and clicks Save through the
// existing CreateExpense path. No persistence happens here.
func (h *Handler) ParseReceipt(w http.ResponseWriter, r *http.Request) {
	owner := auth.OwnerFromContext(r.Context())
	if owner == nil {
		writeReceiptJSON(w, http.StatusUnauthorized, parseReceiptResponse{
			OK: false, Message: "Please sign in again.",
		})
		return
	}

	// Hard cap before parse — saves us from buffering a 200 MB upload
	// just to reject it. ParseMultipartForm reads through MaxBytesReader.
	r.Body = http.MaxBytesReader(w, r.Body, maxReceiptBytes+1<<20)
	if err := r.ParseMultipartForm(maxReceiptBytes + 1<<20); err != nil {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Image too large or upload was interrupted. Max 10 MB.",
		})
		return
	}

	file, fhdr, err := r.FormFile("receipt")
	if err != nil {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "No image attached.",
		})
		return
	}
	defer func() { _ = file.Close() }()
	if fhdr.Size > maxReceiptBytes {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Image too large. Max 10 MB.",
		})
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, maxReceiptBytes+1))
	if err != nil {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Couldn't read the upload. Please try again.",
		})
		return
	}
	if int64(len(data)) > maxReceiptBytes {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Image too large. Max 10 MB.",
		})
		return
	}

	// Sniff the MIME from the bytes — more trustworthy than the client
	// header. http.DetectContentType returns "application/octet-stream"
	// for unknown content; PDFs and HEIC come back correctly.
	mime := canonicalReceiptMIME(http.DetectContentType(data))
	if !receiptMIMEAccepted(mime) {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Unsupported file type. Send a JPG, PNG, WebP, HEIC, or PDF receipt.",
		})
		return
	}

	cfg, engine, err := h.LLMEngine(r.Context())
	if err != nil {
		if errors.Is(err, errLLMNotConfigured) {
			writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
				OK: false, Message: "Set up an LLM provider in Settings → LLM first.",
			})
			return
		}
		slog.Warn("receipt: llm engine load", "error", err)
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Couldn't reach the LLM right now. Please try again.",
		})
		return
	}
	if !llm.ModelSupportsVision(cfg.Provider, cfg.TextModel) {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false,
			Message: "Image uploads need a vision-capable model. Switch providers in Settings → LLM.",
		})
		return
	}
	if accepted := llm.ProviderImageMIMETypes(cfg.Provider); accepted != nil && !accepted[mime] {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false,
			Message: providerMIMEMessage(cfg.Provider, mime),
		})
		return
	}

	loc := models.LoadLocation(owner.Timezone)
	cats, _ := h.Q.ListActiveCategories(r.Context())
	leds, _ := h.Q.ListActiveLedgers(r.Context())

	systemPrompt, err := llm.SystemPrompt(llm.PromptContext{
		CurrencySymbol: owner.CurrencySymbol,
		CurrencyCode:   owner.CurrencyCode,
		Timezone:       owner.Timezone,
		NowRFC3339:     time.Now().In(loc).Format(time.RFC3339),
		Categories:     receiptCategoryNames(cats),
		Ledgers:        receiptLedgerNames(leds),
	})
	if err != nil {
		slog.Warn("receipt: build prompt", "error", err)
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Couldn't build the prompt. Please try again.",
		})
		return
	}

	resp, err := engine.Complete(r.Context(), llm.PurposeParseExpense, llm.Request{
		Model:        cfg.TextModel,
		SystemPrompt: systemPrompt,
		Images:       []llm.ImageInput{{MIMEType: mime, Data: data}},
		JSONMode:     true,
		Temperature:  0,
		MaxTokens:    800,
	})
	if err != nil {
		slog.Warn("receipt: llm call", "error", err)
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Couldn't read the receipt right now. Please try again.",
		})
		return
	}
	parse, err := llm.ParseResponse(resp.Text)
	if err != nil {
		slog.Warn("receipt: parse response", "error", err)
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Couldn't read the receipt. Try a clearer photo.",
		})
		return
	}

	if parse.Intent != "expenses" || len(parse.Expenses) == 0 {
		msg := strings.TrimSpace(parse.Reason)
		if msg == "" {
			msg = "I couldn't read expense details from this image. Try a clearer photo."
		}
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: msg,
		})
		return
	}

	it := parse.Expenses[0]
	if it.Amount == nil || *it.Amount <= 0 {
		writeReceiptJSON(w, http.StatusOK, parseReceiptResponse{
			OK: false, Message: "Couldn't read the total on this receipt. Try a clearer photo.",
		})
		return
	}

	categoryID := receiptMatchCategory(it.CategoryHint, cats)
	ledgerID := receiptMatchLedger(it.LedgerHint, leds)
	when := receiptResolveSpentAt(it.SpentAt, loc)

	out := parseReceiptResponse{
		OK:          true,
		AmountStr:   models.FormatAmount(*it.Amount),
		Description: strings.TrimSpace(it.Description),
		SpentAt:     when.Format("2006-01-02"),
		Confidence:  it.Confidence,
	}
	if categoryID.Valid {
		out.CategoryID = formatNullableID(categoryID)
	}
	if ledgerID.Valid {
		out.LedgerID = formatNullableID(ledgerID)
	}
	writeReceiptJSON(w, http.StatusOK, out)
}

// canonicalReceiptMIME normalizes a content-type header. http.DetectContentType
// returns strings like "image/jpeg; charset=binary" — we strip params for the
// catalog lookup.
func canonicalReceiptMIME(detected string) string {
	if i := strings.Index(detected, ";"); i >= 0 {
		detected = detected[:i]
	}
	return strings.ToLower(strings.TrimSpace(detected))
}

// receiptMIMEAccepted is the global "what does Pennywise accept?" filter.
// Per-provider narrowing happens after this passes.
func receiptMIMEAccepted(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif", "application/pdf":
		return true
	}
	return false
}

// providerMIMEMessage mirrors the Telegram photo handler's per-provider
// hint. Worded for the dashboard (no Markdown escaping needed).
func providerMIMEMessage(provider, gotMIME string) string {
	accepted := "JPG, PNG"
	switch provider {
	case "openai", "anthropic":
		accepted = "JPG, PNG, WebP, or GIF"
	case "gemini":
		accepted = "JPG, PNG, WebP, HEIC, or PDF"
	case "xai":
		accepted = "JPG or PNG"
	}
	return "Your provider (" + provider + ") doesn't accept " + gotMIME +
		". Send a " + accepted + " copy, or switch providers in Settings → LLM."
}

func writeReceiptJSON(w http.ResponseWriter, status int, body parseReceiptResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("receipt: encode response", "error", err)
	}
}

// --- matching helpers (mirror the Telegram dispatcher's; intentionally
//     duplicated to avoid an import cycle and keep each surface free to
//     evolve. Logic is identical today.) -------------------------------

func receiptCategoryNames(cs []sqlcgen.Category) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func receiptLedgerNames(ls []sqlcgen.Ledger) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Name)
	}
	return out
}

func receiptMatchCategory(hint string, cats []sqlcgen.Category) sql.NullInt64 {
	if hint == "" || len(cats) == 0 {
		if hint != "Other" {
			return receiptMatchCategory("Other", cats)
		}
		return sql.NullInt64{}
	}
	pick := fuzzy.Best(hint, receiptCategoryNames(cats))
	if pick == "" {
		if hint != "Other" {
			return receiptMatchCategory("Other", cats)
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

func receiptMatchLedger(hint string, leds []sqlcgen.Ledger) sql.NullInt64 {
	if hint == "" || len(leds) == 0 {
		return sql.NullInt64{}
	}
	pick := fuzzy.Best(hint, receiptLedgerNames(leds))
	if pick == "" {
		return sql.NullInt64{}
	}
	for _, l := range leds {
		if l.Name == pick {
			return sql.NullInt64{Int64: l.ID, Valid: true}
		}
	}
	return sql.NullInt64{}
}

// receiptResolveSpentAt mirrors the Telegram dispatcher's resolveSpentAt
// for the relative-phrase set the prompt is allowed to emit. Anything we
// don't recognize defaults to today.
func receiptResolveSpentAt(s string, loc *time.Location) time.Time {
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
	return now
}

func formatNullableID(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	return strconv.FormatInt(n.Int64, 10)
}
