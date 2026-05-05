package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/testutil"
)

const testChatID int64 = 4242

// dispatcherFixture spins up the full Telegram dispatcher with:
//   - a fresh on-disk SQLite DB with all migrations applied,
//   - a seeded owner + 8 default categories,
//   - a paired telegram_config row,
//   - a llm_config row with provider="openai",
//   - a FakeTelegram httptest server captured via Client.BaseURL,
//   - a MockProvider injected via DispatcherOpts.LLMProvider so the
//     "openai" name still flows but the wire call returns canned JSON.
//
// Tests then call d.Dispatch(ctx, Update{...}) to drive a message and
// assert against captured outbound calls + the DB.
type dispatcherFixture struct {
	t        *testing.T
	d        *Dispatcher
	q        *sqlcgen.Queries
	mockLLM  *testutil.MockProvider
	fakeTG   *testutil.FakeTelegram
	owner    sqlcgen.Owner
	cats     []sqlcgen.Category
	provider string
	model    string
}

func newDispatcherFixture(t *testing.T, providerKey, modelID, mockJSON string) *dispatcherFixture {
	t.Helper()
	db := testutil.NewDB(t)
	q := sqlcgen.New(db)

	owner := testutil.SeedOwner(t, q)
	cats := testutil.SeedDefaultCategories(t, q)
	testutil.SeedLLMConfig(t, q, providerKey, modelID)
	testutil.SeedTelegramConfig(t, q, testChatID)

	fakeTG := testutil.NewFakeTelegram(t)
	mockLLM := &testutil.MockProvider{
		NameStr: providerKey,
		Resp:    testutil.MockResponseJSON(mockJSON),
	}

	client := NewClient("test-token", nil)
	client.BaseURL = fakeTG.Server.URL

	d := NewDispatcher(DispatcherOpts{
		Client:      client,
		Q:           q,
		DB:          db,
		BotUsername: "test_bot",
		LLMTimeout:  5 * time.Second,
		LLMProvider: mockLLM,
		// Secrets intentionally nil — the LLMProvider seam skips the
		// decrypt path. The supervisor (which would need Secrets) isn't
		// involved in dispatcher unit tests.
	})

	return &dispatcherFixture{
		t:        t,
		d:        d,
		q:        q,
		mockLLM:  mockLLM,
		fakeTG:   fakeTG,
		owner:    owner,
		cats:     cats,
		provider: providerKey,
		model:    modelID,
	}
}

// dispatchText injects a free-text message and waits for the dispatcher
// to finish processing. handleMessage runs synchronously, so a single
// Dispatch call is enough.
func (f *dispatcherFixture) dispatchText(text string) {
	f.t.Helper()
	f.d.Dispatch(context.Background(), Update{Message: &Message{
		MessageID: 1,
		Chat:      Chat{ID: testChatID, Type: "private"},
		Date:      time.Now().Unix(),
		Text:      text,
	}})
}

// dispatchPhoto injects a photo Update tied to a registered fake file.
func (f *dispatcherFixture) dispatchPhoto(fileID, caption string) {
	f.t.Helper()
	f.d.Dispatch(context.Background(), Update{Message: &Message{
		MessageID: 2,
		Chat:      Chat{ID: testChatID, Type: "private"},
		Date:      time.Now().Unix(),
		Caption:   caption,
		Photo: []PhotoSize{
			{FileID: fileID, FileUniqueID: "u1", Width: 64, Height: 64, FileSize: 1024},
		},
	}})
}

// expenseCount is a convenience for asserting DB-write side effects.
func (f *dispatcherFixture) expenseCount() int64 {
	f.t.Helper()
	rows, err := f.q.ListRecentExpenses(context.Background(), 1000)
	if err != nil {
		f.t.Fatalf("list expenses: %v", err)
	}
	return int64(len(rows))
}

// containsText returns whether any captured sendMessage/editMessageText
// body contains the given substring (case-sensitive).
func (f *dispatcherFixture) containsText(needle string) bool {
	for _, c := range f.fakeTG.Captured() {
		if c.Method != "sendMessage" && c.Method != "editMessageText" {
			continue
		}
		if txt, _ := c.Body["text"].(string); strings.Contains(txt, needle) {
			return true
		}
	}
	return false
}

// --- Tests ----------------------------------------------------------------

func TestDispatcher_FreeText_SingleExpense(t *testing.T) {
	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 500000,
	    "description": "Fuel",
	    "category_hint": "Transport",
	    "ledger_hint": "",
	    "spent_at": "now",
	    "confidence": 0.95
	  }],
	  "query": null,
	  "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("5000 fuel")

	if got, want := f.expenseCount(), int64(1); got != want {
		t.Fatalf("expense count: got %d, want %d", got, want)
	}
	if f.mockLLM.CallCount() != 1 {
		t.Fatalf("LLM call count: got %d, want 1", f.mockLLM.CallCount())
	}
	if !f.containsText("Logged") {
		t.Fatalf("no Logged confirmation in captured messages: %+v", f.fakeTG.Captured())
	}
}

func TestDispatcher_FreeText_QueryIntent(t *testing.T) {
	const mockJSON = `{
	  "intent": "query",
	  "expenses": [],
	  "query": {"intent": "month", "n": 0, "ledger_hint": ""},
	  "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("how much this month?")

	if got := f.expenseCount(); got != 0 {
		t.Fatalf("expense count after query: got %d, want 0", got)
	}
	// Either a "this month" reply or a no-expenses-yet reply is fine —
	// just assert no expense was logged and a message was sent.
	if f.fakeTG.CountCalls("sendMessage") == 0 {
		t.Fatalf("no sendMessage captured for query intent")
	}
}

func TestDispatcher_FreeText_Unclear(t *testing.T) {
	const mockJSON = `{
	  "intent": "unclear",
	  "expenses": [],
	  "query": null,
	  "reason": "This doesn't look like an expense."
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("good morning")

	if got := f.expenseCount(); got != 0 {
		t.Fatalf("expense count after unclear: got %d, want 0", got)
	}
	if !f.containsText("doesn't look like an expense") {
		t.Fatalf("unclear reason not surfaced; messages=%+v", f.fakeTG.Captured())
	}
}

func TestDispatcher_Photo_VisionCapable(t *testing.T) {
	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 15400,
	    "description": "Receipt at East Repair Inc.",
	    "category_hint": "Shopping",
	    "ledger_hint": "",
	    "spent_at": "today",
	    "confidence": 0.92
	  }],
	  "query": null,
	  "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)
	f.fakeTG.WithFile("photo-id-1", "image/jpeg", []byte("fakeJPEGbytes"))

	f.dispatchPhoto("photo-id-1", "")

	if got, want := f.expenseCount(), int64(1); got != want {
		t.Fatalf("expense count: got %d, want %d", got, want)
	}
	if f.mockLLM.CallCount() != 1 {
		t.Fatalf("LLM should have been called once")
	}
	last := f.mockLLM.LastCall()
	if len(last.Images) != 1 || last.Images[0].MIMEType != "image/jpeg" {
		t.Fatalf("LLM call should carry one JPEG image: %+v", last.Images)
	}
	if !f.containsText("Logged") {
		t.Fatalf("no Logged confirmation; captured=%+v", f.fakeTG.Captured())
	}
}

func TestDispatcher_Photo_VisionIncapableModel(t *testing.T) {
	// Use an unknown provider/model so ModelSupportsVision falls back to
	// its provider-default branch — but we need a provider whose default
	// is text-only. Today, all four catalog providers have Vision=true.
	// Instead, simulate "unknown provider" by seeding a provider name
	// the catalog doesn't recognize at all → ModelSupportsVision returns
	// false (its "unknown provider" branch).
	f := newDispatcherFixture(t, "unknown-provider", "any-model", "")
	f.fakeTG.WithFile("photo-id-2", "image/jpeg", []byte("fakeJPEGbytes"))

	f.dispatchPhoto("photo-id-2", "")

	if got := f.expenseCount(); got != 0 {
		t.Fatalf("no expense should be logged when vision is unsupported; got %d", got)
	}
	if f.mockLLM.CallCount() != 0 {
		t.Fatalf("LLM should not be called when vision is unsupported")
	}
	if !f.containsText("vision-capable model") {
		t.Fatalf("expected vision-capable hint; got=%+v", f.fakeTG.Captured())
	}
}

func TestDispatcher_Photo_ProviderMIMEMismatch(t *testing.T) {
	f := newDispatcherFixture(t, "xai", "grok-4-1-fast-non-reasoning", "")
	// xAI accepts only JPG/PNG. Send a WebP via document — but our
	// dispatcher gates documents through pickImageAttachment, which
	// requires the document MIME field. Photos are JPEG-only by Telegram,
	// so to test webp we send a document.
	f.fakeTG.WithFile("doc-id-1", "image/webp", []byte("fakeWebPbytes"))
	f.d.Dispatch(context.Background(), Update{Message: &Message{
		MessageID: 3,
		Chat:      Chat{ID: testChatID, Type: "private"},
		Date:      time.Now().Unix(),
		Document: &Document{
			FileID:   "doc-id-1",
			FileName: "receipt.webp",
			MIMEType: "image/webp",
			FileSize: 2048,
		},
	}})

	if got := f.expenseCount(); got != 0 {
		t.Fatalf("no expense should be logged on MIME mismatch; got %d", got)
	}
	if f.mockLLM.CallCount() != 0 {
		t.Fatalf("LLM should not be called on MIME mismatch")
	}
	if !f.containsText("doesn't accept") {
		t.Fatalf("expected provider-MIME hint; got=%+v", f.fakeTG.Captured())
	}
}

// --- Low-confidence confirm flow -----------------------------------------

func TestDispatcher_LowConfidence_PromptsAndCommitsOnYes(t *testing.T) {
	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 300000,
	    "description": "Paid",
	    "category_hint": "Other",
	    "ledger_hint": "",
	    "spent_at": "now",
	    "confidence": 0.55
	  }],
	  "query": null,
	  "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("paid 3000")

	// Prompt sent — no DB write yet.
	if got := f.expenseCount(); got != 0 {
		t.Fatalf("expense should not be committed before Yes; got %d", got)
	}
	if !f.containsText("Log this?") {
		t.Fatalf("expected low-confidence prompt; messages=%+v", f.fakeTG.Captured())
	}

	// Simulate the user tapping Yes.
	f.d.Dispatch(context.Background(), Update{CallbackQuery: &CallbackQuery{
		ID:      "cq-1",
		From:    User{ID: 1},
		Data:    "pxe:y",
		Message: &Message{MessageID: 1001, Chat: Chat{ID: testChatID, Type: "private"}},
	}})

	if got, want := f.expenseCount(), int64(1); got != want {
		t.Fatalf("expense should be committed after Yes; got %d, want %d", got, want)
	}
}

func TestDispatcher_LowConfidence_CancelDiscards(t *testing.T) {
	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 300000, "description": "Paid",
	    "category_hint": "Other", "ledger_hint": "",
	    "spent_at": "now", "confidence": 0.40
	  }],
	  "query": null, "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("paid 3000")

	f.d.Dispatch(context.Background(), Update{CallbackQuery: &CallbackQuery{
		ID: "cq-1", From: User{ID: 1}, Data: "pxe:n",
		Message: &Message{MessageID: 1001, Chat: Chat{ID: testChatID, Type: "private"}},
	}})

	if got := f.expenseCount(); got != 0 {
		t.Fatalf("expense should not be committed after Cancel; got %d", got)
	}
	if !f.containsText("Discarded") {
		t.Fatalf("expected Discarded message; got %+v", f.fakeTG.Captured())
	}
}

func TestDispatcher_LowConfidence_ZeroConfidenceAutoCommits(t *testing.T) {
	// Confidence == 0 is treated as "no signal", not "low" — auto-commit
	// to avoid prompting on every message if the provider drops the field.
	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 500000, "description": "Fuel",
	    "category_hint": "Transport", "ledger_hint": "",
	    "spent_at": "now", "confidence": 0
	  }],
	  "query": null, "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("5000 fuel")

	if got, want := f.expenseCount(), int64(1); got != want {
		t.Fatalf("zero-confidence should auto-commit; got %d, want %d", got, want)
	}
	if f.containsText("Log this?") {
		t.Fatalf("zero-confidence must not produce a prompt")
	}
}

func TestDispatcher_HighConfidence_NoPrompt(t *testing.T) {
	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 1250, "description": "Coffee",
	    "category_hint": "Food", "ledger_hint": "",
	    "spent_at": "now", "confidence": 0.92
	  }],
	  "query": null, "reason": ""
	}`
	f := newDispatcherFixture(t, "openai", "gpt-5.4-nano", mockJSON)

	f.dispatchText("12.50 coffee")

	if got, want := f.expenseCount(), int64(1); got != want {
		t.Fatalf("high-confidence should auto-commit; got %d, want %d", got, want)
	}
	if f.containsText("Log this?") {
		t.Fatalf("high-confidence must not produce a prompt")
	}
}

func TestDispatcher_NoLLMConfigured(t *testing.T) {
	// Skip SeedLLMConfig — the row simply doesn't exist.
	db := testutil.NewDB(t)
	q := sqlcgen.New(db)
	_ = testutil.SeedOwner(t, q)
	_ = testutil.SeedDefaultCategories(t, q)
	testutil.SeedTelegramConfig(t, q, testChatID)

	fakeTG := testutil.NewFakeTelegram(t)
	mockLLM := &testutil.MockProvider{NameStr: "mock"}
	client := NewClient("test-token", nil)
	client.BaseURL = fakeTG.Server.URL

	d := NewDispatcher(DispatcherOpts{
		Client: client, Q: q, DB: db,
		BotUsername: "test_bot", LLMTimeout: 5 * time.Second,
		LLMProvider: mockLLM,
	})

	d.Dispatch(context.Background(), Update{Message: &Message{
		MessageID: 1,
		Chat:      Chat{ID: testChatID, Type: "private"},
		Date:      time.Now().Unix(),
		Text:      "5000 fuel",
	}})

	if mockLLM.CallCount() != 0 {
		t.Fatalf("LLM should not be called when llm_config is missing")
	}
	// The setup-hint reply mentions "Settings → LLM Provider".
	captured := fakeTG.Captured()
	found := false
	for _, c := range captured {
		txt, _ := c.Body["text"].(string)
		if strings.Contains(txt, "Settings") && strings.Contains(txt, "LLM") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected setup hint mentioning Settings → LLM; got %+v", captured)
	}
}
