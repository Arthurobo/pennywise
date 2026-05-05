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

func TestGemini_RequestShape_JSONMode(t *testing.T) {
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Method = r.Method
		rec.Path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rec.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{{"text": `{"intent":"expense"}`}}}},
			},
			"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5},
		})
	}))
	defer srv.Close()

	p := &GeminiProvider{APIKey: "ABC", BaseURL: srv.URL, Client: srv.Client()}
	resp, err := p.Complete(context.Background(), Request{
		Model:        "gemini-2.0-flash",
		SystemPrompt: "sys",
		UserMessage:  "5000 fuel",
		JSONMode:     true,
		Temperature:  0,
		MaxTokens:    256,
	})
	require.NoError(t, err)
	assert.Equal(t, "/models/gemini-2.0-flash:generateContent", rec.Path)

	gc, ok := rec.Body["generationConfig"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "application/json", gc["responseMimeType"])

	si, ok := rec.Body["systemInstruction"].(map[string]any)
	require.True(t, ok)
	parts, _ := si["parts"].([]any)
	require.Len(t, parts, 1)
	assert.Equal(t, "sys", parts[0].(map[string]any)["text"])

	assert.Equal(t, `{"intent":"expense"}`, resp.Text)
	assert.Equal(t, 10, resp.InputTokens)
	assert.Equal(t, 5, resp.OutputTokens)
}

func TestGemini_PassesAPIKeyInQuery(t *testing.T) {
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}}},
			},
		})
	}))
	defer srv.Close()
	p := &GeminiProvider{APIKey: "secret-key", BaseURL: srv.URL, Client: srv.Client()}
	_, err := p.Complete(context.Background(), Request{Model: "gemini-2.0-flash", UserMessage: "hi"})
	require.NoError(t, err)
	assert.Equal(t, "secret-key", seenKey)
}
