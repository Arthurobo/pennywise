package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/handlers"
	"github.com/Arthurobo/pennywise/internal/llm"
	"github.com/Arthurobo/pennywise/internal/testutil"
)

// fakeJPEG returns bytes that http.DetectContentType recognizes as
// image/jpeg. The first three bytes (FF D8 FF) are the JPEG SOI marker;
// nothing else here needs to actually decode.
func fakeJPEG() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
}

// fakeWebP returns bytes that http.DetectContentType identifies as image/webp.
func fakeWebP() []byte {
	out := make([]byte, 0, 32)
	out = append(out, 'R', 'I', 'F', 'F') // RIFF header
	out = append(out, 0x20, 0x00, 0x00, 0x00) // size — placeholder
	out = append(out, 'W', 'E', 'B', 'P')
	out = append(out, 'V', 'P', '8', ' ')
	out = append(out, make([]byte, 16)...) // padding
	return out
}

// uploadReceipt issues a multipart POST to /expenses/parse-receipt and
// returns the decoded JSON response.
func uploadReceipt(t *testing.T, client *http.Client, ts *httptest.Server, csrf, filename, mime string, body []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="receipt"; filename="` + filename + `"`}
	hdr["Content-Type"] = []string{mime}
	part, err := mw.CreatePart(hdr)
	require.NoError(t, err)
	_, _ = part.Write(body)
	require.NoError(t, mw.Close())

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/expenses/parse-receipt", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", csrf)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// extractCSRFFromAuthed pulls the CSRF token from any rendered page —
// /expenses/new is convenient since it's already gated by auth.
func extractCSRFFromAuthed(t *testing.T, client *http.Client, ts *httptest.Server) string {
	t.Helper()
	resp, err := client.Get(ts.URL + "/expenses/new")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return extractCSRF(t, string(body))
}

// installMockLLM seeds an llm_config row + injects a MockProvider on the
// Handler. Returns the mock so tests can assert on calls.
func installMockLLM(t *testing.T, h *handlers.Handler, q *sqlcgen.Queries, providerKey, modelID, mockJSON string) *testutil.MockProvider {
	t.Helper()
	testutil.SeedLLMConfig(t, q, providerKey, modelID)
	mock := &testutil.MockProvider{
		NameStr: providerKey,
		Resp:    testutil.MockResponseJSON(mockJSON),
	}
	h.LLMProvider = mock
	return mock
}

func TestParseReceipt_HappyPath(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	q := sqlcgen.New(h.DB)

	const mockJSON = `{
	  "intent": "expenses",
	  "expenses": [{
	    "amount": 12500,
	    "description": "Lunch at the kiosk",
	    "category_hint": "Food",
	    "ledger_hint": "",
	    "spent_at": "today",
	    "confidence": 0.92
	  }],
	  "query": null,
	  "reason": ""
	}`
	mock := installMockLLM(t, h, q, "openai", "gpt-5.4-nano", mockJSON)
	csrf := extractCSRFFromAuthed(t, client, ts)

	status, out := uploadReceipt(t, client, ts, csrf, "receipt.jpg", "image/jpeg", fakeJPEG())
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, out["ok"], "ok=true expected; full response=%+v", out)
	assert.Contains(t, out["description"], "Lunch")
	assert.NotEmpty(t, out["amount_str"])
	assert.NotEmpty(t, out["spent_at"])
	assert.Equal(t, 1, mock.CallCount())
	last := mock.LastCall()
	require.Len(t, last.Images, 1)
	assert.Equal(t, "image/jpeg", last.Images[0].MIMEType)
}

func TestParseReceipt_OversizeFile(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	q := sqlcgen.New(h.DB)
	_ = installMockLLM(t, h, q, "openai", "gpt-5.4-nano", "{}")
	csrf := extractCSRFFromAuthed(t, client, ts)

	// 11 MiB exceeds the 10 MiB cap. ParseReceipt should reject before
	// the LLM call lands.
	big := make([]byte, 11*1024*1024)
	copy(big, fakeJPEG())

	status, out := uploadReceipt(t, client, ts, csrf, "huge.jpg", "image/jpeg", big)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, out["ok"])
	msg, _ := out["message"].(string)
	assert.True(t, strings.Contains(msg, "10 MB") || strings.Contains(msg, "too large"),
		"expected oversize message; got %q", msg)
}

func TestParseReceipt_WrongMIME(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	q := sqlcgen.New(h.DB)
	mock := installMockLLM(t, h, q, "openai", "gpt-5.4-nano", "{}")
	csrf := extractCSRFFromAuthed(t, client, ts)

	status, out := uploadReceipt(t, client, ts, csrf, "notes.txt", "text/plain", []byte("not an image"))
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, out["ok"])
	msg, _ := out["message"].(string)
	assert.Contains(t, msg, "Unsupported")
	assert.Equal(t, 0, mock.CallCount(), "LLM must not be called on wrong MIME")
}

func TestParseReceipt_ProviderMIMEMismatch(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	q := sqlcgen.New(h.DB)
	mock := installMockLLM(t, h, q, "xai", "grok-4-1-fast-non-reasoning", "{}")
	csrf := extractCSRFFromAuthed(t, client, ts)

	status, out := uploadReceipt(t, client, ts, csrf, "receipt.webp", "image/webp", fakeWebP())
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, out["ok"], "ok=false expected on provider MIME mismatch")
	msg, _ := out["message"].(string)
	// Message should name the provider's accepted formats.
	assert.True(t, strings.Contains(msg, "JPG") && strings.Contains(msg, "PNG"),
		"expected provider-specific message naming JPG/PNG; got %q", msg)
	assert.Equal(t, 0, mock.CallCount(), "LLM must not be called on provider mismatch")
}

func TestParseReceipt_VisionIncapableModel(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	q := sqlcgen.New(h.DB)
	mock := installMockLLM(t, h, q, "unknown-provider", "any-model", "{}")
	csrf := extractCSRFFromAuthed(t, client, ts)

	status, out := uploadReceipt(t, client, ts, csrf, "receipt.jpg", "image/jpeg", fakeJPEG())
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, out["ok"])
	msg, _ := out["message"].(string)
	assert.Contains(t, msg, "vision-capable")
	assert.Equal(t, 0, mock.CallCount())
}

func TestParseReceipt_NoLLMConfigured(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	// Note: no installMockLLM call — llm_config row doesn't exist.
	// We still need to inject SOMETHING into LLMProvider for the test
	// not to take the decrypt path on the (non-existent) row. But the
	// handler bails on errLLMNotConfigured before reaching the override.
	// So leave LLMProvider nil; the handler's own missing-row check
	// returns the "Set up an LLM provider…" message.
	_ = h
	csrf := extractCSRFFromAuthed(t, client, ts)

	status, out := uploadReceipt(t, client, ts, csrf, "receipt.jpg", "image/jpeg", fakeJPEG())
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, out["ok"])
	msg, _ := out["message"].(string)
	assert.Contains(t, strings.ToLower(msg), "llm")
}

func TestParseReceipt_LLMReturnsUnclear(t *testing.T) {
	ts, h := bootTestServer(t)
	client := completeSetup(t, ts)
	q := sqlcgen.New(h.DB)
	const mockJSON = `{
	  "intent": "unclear",
	  "expenses": [],
	  "query": null,
	  "reason": "This isn't a receipt."
	}`
	mock := installMockLLM(t, h, q, "openai", "gpt-5.4-nano", mockJSON)
	csrf := extractCSRFFromAuthed(t, client, ts)

	status, out := uploadReceipt(t, client, ts, csrf, "selfie.jpg", "image/jpeg", fakeJPEG())
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, out["ok"])
	msg, _ := out["message"].(string)
	assert.Contains(t, msg, "isn't a receipt")
	assert.Equal(t, 1, mock.CallCount())
}

// quiet the unused-import linter when the file has no compile-time refs.
var _ = context.Background
var _ = time.Now
var _ llm.Provider = (*testutil.MockProvider)(nil)
