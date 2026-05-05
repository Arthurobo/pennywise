package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFakeAnthropic(t *testing.T, status int, respBody any) (*httptest.Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Method = r.Method
		rec.Path = r.URL.Path
		rec.Auth = r.Header.Get("x-api-key")
		rec.ContentTyp = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rec.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestAnthropic_PrefillTechnique(t *testing.T) {
	srv, rec := newFakeAnthropic(t, 200, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": `"intent":"expense","amount":500000}`},
		},
		"usage": map[string]any{"input_tokens": 12, "output_tokens": 8},
	})
	p := &AnthropicProvider{APIKey: "sk-ant", BaseURL: srv.URL, Client: srv.Client()}
	resp, err := p.Complete(context.Background(), Request{
		Model:        "claude-haiku-4-5",
		SystemPrompt: "system",
		UserMessage:  "5000 fuel",
		JSONMode:     true,
	})
	require.NoError(t, err)

	msgs, _ := rec.Body["messages"].([]any)
	require.Len(t, msgs, 2, "JSON mode must append a prefill assistant turn")
	last := msgs[len(msgs)-1].(map[string]any)
	assert.Equal(t, "assistant", last["role"])
	assert.Equal(t, "{", last["content"])

	// The provider must re-prepend `{` to the model's continuation so the
	// caller sees a complete JSON object.
	assert.True(t, len(resp.Text) > 0 && resp.Text[0] == '{', "got: %q", resp.Text)
	assert.Contains(t, resp.Text, `"intent":"expense"`)
}

func TestAnthropic_NoPrefillWhenJSONModeOff(t *testing.T) {
	srv, rec := newFakeAnthropic(t, 200, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "hello"}},
	})
	p := &AnthropicProvider{APIKey: "sk", BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Complete(context.Background(), Request{Model: "claude-haiku-4-5", UserMessage: "hi"})
	require.NoError(t, err)

	msgs, _ := rec.Body["messages"].([]any)
	require.Len(t, msgs, 1)
	first := msgs[0].(map[string]any)
	assert.Equal(t, "user", first["role"])
}

func TestAnthropic_SetsRequiredHeaders(t *testing.T) {
	srv, rec := newFakeAnthropic(t, 200, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "ok"}},
	})
	p := &AnthropicProvider{APIKey: "k1", BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Complete(context.Background(), Request{Model: "claude-haiku-4-5", UserMessage: "hi"})
	require.NoError(t, err)
	assert.Equal(t, "k1", rec.Auth, "x-api-key must carry the key")
}
