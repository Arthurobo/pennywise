package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOpenAIServer captures the inbound request and returns whatever body the
// test wants. Useful for verifying request shape (JSON mode, model, headers)
// without hitting the real API.
type recordedRequest struct {
	Method     string
	Path       string
	Auth       string
	ContentTyp string
	Body       map[string]any
}

func newFakeOpenAI(t *testing.T, status int, respBody any) (*httptest.Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Method = r.Method
		rec.Path = r.URL.Path
		rec.Auth = r.Header.Get("Authorization")
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

func TestOpenAI_RequestShape(t *testing.T) {
	srv, rec := newFakeOpenAI(t, 200, map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": `{"intent":"expense"}`}},
		},
		"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 8},
	})

	p := &OpenAIProvider{APIKey: "sk-test", BaseURL: srv.URL, Client: srv.Client()}
	resp, err := p.Complete(context.Background(), Request{
		Model:        "gpt-4o-mini",
		SystemPrompt: "sys",
		UserMessage:  "5000 fuel",
		JSONMode:     true,
		Temperature:  0,
		MaxTokens:    256,
	})
	require.NoError(t, err)

	assert.Equal(t, "POST", rec.Method)
	assert.Equal(t, "/chat/completions", rec.Path)
	assert.Equal(t, "Bearer sk-test", rec.Auth)
	assert.Equal(t, "application/json", rec.ContentTyp)
	assert.Equal(t, "gpt-4o-mini", rec.Body["model"])

	rf, ok := rec.Body["response_format"].(map[string]any)
	require.True(t, ok, "JSONMode should set response_format")
	assert.Equal(t, "json_object", rf["type"])

	msgs, _ := rec.Body["messages"].([]any)
	require.Len(t, msgs, 2)
	first := msgs[0].(map[string]any)
	assert.Equal(t, "system", first["role"])
	assert.Equal(t, "sys", first["content"])

	assert.Equal(t, `{"intent":"expense"}`, resp.Text)
	assert.Equal(t, 12, resp.InputTokens)
	assert.Equal(t, 8, resp.OutputTokens)
}

func TestOpenAI_OmitsResponseFormatWhenNotJSON(t *testing.T) {
	srv, rec := newFakeOpenAI(t, 200, map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": "ok"}},
		},
	})
	p := &OpenAIProvider{APIKey: "sk", BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Complete(context.Background(), Request{Model: "gpt-4o-mini", SystemPrompt: "x", UserMessage: "y"})
	require.NoError(t, err)
	_, hasRF := rec.Body["response_format"]
	assert.False(t, hasRF, "non-JSON requests must not include response_format")
}

func TestOpenAI_PropagatesAPIError(t *testing.T) {
	srv, _ := newFakeOpenAI(t, 401, map[string]any{
		"error": map[string]any{"message": "Invalid API key", "type": "invalid_request_error"},
	})
	p := &OpenAIProvider{APIKey: "bad", BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Complete(context.Background(), Request{Model: "gpt-4o-mini"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid API key")
	assert.Contains(t, err.Error(), "401")
}

func TestOpenAI_Test_SendsTinyProbe(t *testing.T) {
	srv, rec := newFakeOpenAI(t, 200, map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": "OK"}},
		},
	})
	p := &OpenAIProvider{APIKey: "sk", BaseURL: srv.URL, Client: srv.Client()}
	require.NoError(t, p.Test(context.Background(), "gpt-4o-mini"))

	mt, _ := rec.Body["max_tokens"].(float64)
	assert.LessOrEqual(t, mt, 16.0, "Test should ask for very few tokens")
}

func TestOpenAI_RespectsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow server: sleep past the caller's timeout.
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(200)
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "sk", BaseURL: srv.URL, Client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Complete(ctx, Request{Model: "gpt-4o-mini"})
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "context deadline") || strings.Contains(err.Error(), "deadline exceeded"),
		"got: %v", err)
}
