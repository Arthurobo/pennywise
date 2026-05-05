package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Telegram Bot API root. Token is interpolated by Client.endpoint.
const tgBaseURL = "https://api.telegram.org"

// ErrUnauthorized is returned when Telegram replies 401 — the token is wrong
// or has been revoked. The supervisor uses this to disable the bot.
var ErrUnauthorized = errors.New("telegram: 401 unauthorized")

// Client wraps the Telegram Bot API endpoints we use. It also enforces a
// per-chat outbound rate limit (1 msg/s per chat — Telegram's documented
// limit) so a runaway loop can't get the bot temporarily blocked.
type Client struct {
	token string
	http  *http.Client
	limit *rateLimiter

	// BaseURL overrides the Telegram API root. Empty means use the default
	// (tgBaseURL). Mirrors the OpenAIProvider.BaseURL pattern; tests point
	// at httptest.Server.URL. Production callers leave it unset.
	BaseURL string
}

// NewClient constructs a Client. If httpc is nil, a sensible default is used.
func NewClient(token string, httpc *http.Client) *Client {
	if httpc == nil {
		httpc = &http.Client{Timeout: 65 * time.Second}
	}
	return &Client{
		token: token,
		http:  httpc,
		limit: newRateLimiter(time.Second, 1),
	}
}

// baseURL returns the configured override or the package default.
func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return tgBaseURL
}

// endpoint builds the full URL for a method. We don't include the token in
// the URL when constructing — but the wire-level call must include it,
// since Telegram authenticates by URL path (https://api.telegram.org/bot<token>/method).
func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.baseURL(), c.token, method)
}

// Update is the union-shape of an incoming Telegram update. Only the
// subset of fields we care about is modeled; the rest is ignored.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from,omitempty"`
	Chat      Chat        `json:"chat"`
	Date      int64       `json:"date"`
	Text      string      `json:"text,omitempty"`
	Caption   string      `json:"caption,omitempty"`
	Voice     any         `json:"voice,omitempty"`
	Audio     any         `json:"audio,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
	Document  *Document   `json:"document,omitempty"`
	ReplyTo   *Message    `json:"reply_to_message,omitempty"`
}

// PhotoSize is one resolution of a photo. Telegram sends an array sorted
// from smallest to largest; the dispatcher picks the last entry to feed
// the highest-resolution copy to the vision model.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// Document is an arbitrary file attachment. Receipts sent as PDFs or
// HEIC images arrive as documents rather than photos. MIMEType is the
// authoritative content-type; FileName is hint-only.
type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// SendMessageOpts captures the optional kwargs of sendMessage.
type SendMessageOpts struct {
	ParseMode             string          `json:"parse_mode,omitempty"`
	ReplyMarkup           *InlineKeyboard `json:"reply_markup,omitempty"`
	DisableWebPagePreview bool            `json:"disable_web_page_preview,omitempty"`
	ReplyToMessageID      int64           `json:"reply_to_message_id,omitempty"`
}

// InlineKeyboard mirrors Telegram's reply_markup -> inline_keyboard.
type InlineKeyboard struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// InlineKeyboardButton matches Telegram's button shape. Either CallbackData
// or URL is set, not both.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// GetMe is used to validate a token and fetch the bot's username.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var raw struct {
		OK     bool `json:"ok"`
		Result User `json:"result"`
	}
	if err := c.callJSON(ctx, "getMe", nil, &raw); err != nil {
		return User{}, err
	}
	return raw.Result, nil
}

// GetUpdates long-polls for new updates. Pass timeout up to ~50s.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	body := map[string]any{
		"offset":          offset,
		"timeout":         int(timeout / time.Second),
		"allowed_updates": []string{"message", "callback_query"},
	}
	var raw struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := c.callJSON(ctx, "getUpdates", body, &raw); err != nil {
		return nil, err
	}
	return raw.Result, nil
}

// SendMessage posts a text message to chatID, observing the per-chat rate limit.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, opts *SendMessageOpts) (Message, error) {
	c.limit.wait(chatID)
	body := map[string]any{"chat_id": chatID, "text": text}
	if opts != nil {
		if opts.ParseMode != "" {
			body["parse_mode"] = opts.ParseMode
		}
		if opts.ReplyMarkup != nil {
			body["reply_markup"] = opts.ReplyMarkup
		}
		if opts.DisableWebPagePreview {
			body["disable_web_page_preview"] = true
		}
		if opts.ReplyToMessageID > 0 {
			body["reply_to_message_id"] = opts.ReplyToMessageID
		}
	}
	var raw struct {
		OK     bool    `json:"ok"`
		Result Message `json:"result"`
	}
	if err := c.callJSON(ctx, "sendMessage", body, &raw); err != nil {
		return Message{}, err
	}
	return raw.Result, nil
}

// EditMessageText edits a previously-sent message in place.
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, opts *SendMessageOpts) error {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if opts != nil {
		if opts.ParseMode != "" {
			body["parse_mode"] = opts.ParseMode
		}
		if opts.ReplyMarkup != nil {
			body["reply_markup"] = opts.ReplyMarkup
		}
	}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "editMessageText", body, &raw)
}

// AnswerCallbackQuery acknowledges a callback. text shows as a toast/popup.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	body := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		body["text"] = text
	}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "answerCallbackQuery", body, &raw)
}

// BotCommand is one entry in the bot's slash-command menu shown by Telegram.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetMyCommands registers the slash-command menu Telegram surfaces in the
// `/` autocomplete and the menu button. Idempotent — safe to call on every
// bot start.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	body := map[string]any{"commands": commands}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "setMyCommands", body, &raw)
}

// SetMyDescription sets the long welcome text shown on the bot's chat-open
// screen before any messages exist.
func (c *Client) SetMyDescription(ctx context.Context, description string) error {
	body := map[string]any{"description": description}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "setMyDescription", body, &raw)
}

// SetMyShortDescription sets the one-liner shown in Telegram search results.
func (c *Client) SetMyShortDescription(ctx context.Context, description string) error {
	body := map[string]any{"short_description": description}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "setMyShortDescription", body, &raw)
}

// DeleteMessage removes a message from the chat. We use this to clean up the
// ledger picker after a selection is made.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "deleteMessage", body, &raw)
}

// PinChatMessage pins messageID in chatID.
func (c *Client) PinChatMessage(ctx context.Context, chatID, messageID int64) error {
	body := map[string]any{
		"chat_id":              chatID,
		"message_id":           messageID,
		"disable_notification": true,
	}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "pinChatMessage", body, &raw)
}

// UnpinAllChatMessages clears every pin in the chat. We use the broad form
// since the bot may have set the active-ledger pin a long time ago and we
// don't reliably remember its message_id.
func (c *Client) UnpinAllChatMessages(ctx context.Context, chatID int64) error {
	body := map[string]any{"chat_id": chatID}
	var raw struct{ OK bool }
	return c.callJSON(ctx, "unpinAllChatMessages", body, &raw)
}

// File is the result of getFile: a relative path on Telegram's CDN that
// can be appended to the file-download URL.
type File struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

// GetFile resolves a Telegram file_id to a downloadable path. The path
// expires after one hour, so callers should immediately follow up with
// DownloadFile.
func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	body := map[string]any{"file_id": fileID}
	var raw struct {
		OK     bool `json:"ok"`
		Result File `json:"result"`
	}
	if err := c.callJSON(ctx, "getFile", body, &raw); err != nil {
		return File{}, err
	}
	return raw.Result, nil
}

// DownloadFile fetches the binary content for a previously-resolved file
// path. maxBytes caps the response body so a malicious or oversized
// attachment can't run the bot out of memory; pass 0 for the package
// default. Returns ErrFileTooLarge if the response exceeds the cap.
func (c *Client) DownloadFile(ctx context.Context, filePath string, maxBytes int64) ([]byte, error) {
	if filePath == "" {
		return nil, errors.New("telegram: empty file_path")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxAttachmentBytes
	}
	url := fmt.Sprintf("%s/file/bot%s/%s", c.baseURL(), c.token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram getFile download: build: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram getFile download: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("telegram getFile download: HTTP %d: %s", resp.StatusCode, string(raw))
	}
	// Read one byte past the cap so we can distinguish "exactly at cap" from
	// "larger than cap" deterministically.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("telegram getFile download: read body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrFileTooLarge
	}
	return body, nil
}

// DefaultMaxAttachmentBytes caps a single inbound attachment to 10 MiB.
// Telegram itself caps photo downloads via getFile at 20 MiB; tightening to
// 10 MiB keeps base64-encoded LLM payloads under the typical provider
// request-size limits with headroom for the prompt + caption.
const DefaultMaxAttachmentBytes int64 = 10 * 1024 * 1024

// ErrFileTooLarge is returned when a downloaded attachment exceeds the cap.
var ErrFileTooLarge = errors.New("telegram: attachment exceeds size limit")

// callJSON posts JSON to method and decodes the response into out. Decodes
// `description` from any error payload so callers see Telegram's reason.
func (c *Client) callJSON(ctx context.Context, method string, body any, out any) error {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("telegram %s: encode: %w", method, err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), buf)
	if err != nil {
		return fmt.Errorf("telegram %s: build: %w", method, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: http: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		var e struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Description != "" {
			return fmt.Errorf("telegram %s: HTTP %d: %s", method, resp.StatusCode, e.Description)
		}
		return fmt.Errorf("telegram %s: HTTP %d: %s", method, resp.StatusCode, snippet(raw, 200))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("telegram %s: decode: %w", method, err)
		}
	}
	return nil
}

// ValidateToken hits getMe directly. Used by the settings handler before
// persisting the token, so an obviously-bad token gets rejected with a
// clear error message.
func ValidateToken(ctx context.Context, token string, httpc *http.Client) (User, error) {
	c := NewClient(token, httpc)
	return c.GetMe(ctx)
}

// EscapeMarkdown escapes the v1-Markdown reserved chars so user-supplied
// text in confirmation messages can't break formatting. Limited to the
// characters Telegram's Markdown (not MarkdownV2) treats as special.
func EscapeMarkdown(s string) string {
	r := strings.NewReplacer("_", "\\_", "*", "\\*", "`", "\\`", "[", "\\[")
	return r.Replace(s)
}

// rateLimiter is a per-key token bucket. We use it to throttle outbound
// messages per chat ID at 1 msg/s. The map grows unbounded in theory; in
// practice a single-tenant bot only ever sees one chat ID, so OK.
type rateLimiter struct {
	mu     sync.Mutex
	gap    time.Duration
	burst  int
	last   map[int64]time.Time
	tokens map[int64]int
}

func newRateLimiter(gap time.Duration, burst int) *rateLimiter {
	return &rateLimiter{
		gap:    gap,
		burst:  burst,
		last:   map[int64]time.Time{},
		tokens: map[int64]int{},
	}
}

func (r *rateLimiter) wait(key int64) {
	for {
		r.mu.Lock()
		now := time.Now()
		last, ok := r.last[key]
		if !ok {
			r.last[key] = now
			r.tokens[key] = r.burst - 1
			r.mu.Unlock()
			return
		}
		// Refill tokens based on elapsed time.
		elapsed := now.Sub(last)
		gain := int(elapsed / r.gap)
		if gain > 0 {
			r.tokens[key] += gain
			if r.tokens[key] > r.burst {
				r.tokens[key] = r.burst
			}
			r.last[key] = last.Add(time.Duration(gain) * r.gap)
		}
		if r.tokens[key] > 0 {
			r.tokens[key]--
			r.mu.Unlock()
			return
		}
		// Compute how long to sleep before the next token.
		nextRefill := r.last[key].Add(r.gap)
		wait := time.Until(nextRefill)
		r.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
	}
}

func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// queryEscape is a tiny wrapper so callers don't have to import net/url.
func queryEscape(s string) string { return url.QueryEscape(s) }
