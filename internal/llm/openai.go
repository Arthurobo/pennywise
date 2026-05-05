package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OpenAIBaseURL is the default REST root.
const OpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIProvider wraps the /chat/completions endpoint.
type OpenAIProvider struct {
	APIKey  string
	BaseURL string // empty → OpenAIBaseURL
	Client  *http.Client
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return OpenAIBaseURL
}

func (p *OpenAIProvider) httpClient() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// openAIMessage.Content is either a plain string (text-only requests) or a
// slice of openAIContentPart when an image is attached. The OpenAI wire
// format accepts both shapes; using `any` keeps the JSON encoding clean
// without forcing every text-only call to allocate a part slice.
type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIContentPart struct {
	Type     string             `json:"type"`
	Text     string             `json:"text,omitempty"`
	ImageURL *openAIImageURLRef `json:"image_url,omitempty"`
}

type openAIImageURLRef struct {
	URL string `json:"url"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	Temperature    float64               `json:"temperature"`
	MaxTokens      int                   `json:"max_tokens,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

// openAIResponseMessage is the assistant-turn shape on the response side.
// Replies are always plain text — the array form only exists on requests —
// so Content is a string here, distinct from openAIMessage.Content (any).
type openAIResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message      openAIResponseMessage `json:"message"`
		FinishReason string                `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *openAIError `json:"error"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// Complete calls /chat/completions.
func (p *OpenAIProvider) Complete(ctx context.Context, r Request) (Response, error) {
	userContent, err := openAIUserContent(r)
	if err != nil {
		return Response{}, err
	}
	body := openAIRequest{
		Model:       r.Model,
		Temperature: r.Temperature,
		MaxTokens:   r.MaxTokens,
		Messages: []openAIMessage{
			{Role: "system", Content: r.SystemPrompt},
			{Role: "user", Content: userContent},
		},
	}
	if r.JSONMode {
		body.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
	}
	raw, err := p.do(ctx, "/chat/completions", body)
	if err != nil {
		return Response{}, err
	}
	var resp openAIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if resp.Error != nil {
		return Response{}, fmt.Errorf("openai: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: no choices in response")
	}
	return Response{
		Text:         resp.Choices[0].Message.Content,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}, nil
}

// Test sends a minimal completion to verify the key works.
func (p *OpenAIProvider) Test(ctx context.Context, model string) error {
	if model == "" {
		model = DefaultModel("openai")
	}
	_, err := p.Complete(ctx, Request{
		Model:        model,
		SystemPrompt: "You are a connectivity test. Reply with exactly: OK",
		UserMessage:  "ping",
		Temperature:  0,
		MaxTokens:    8,
	})
	return err
}

func (p *OpenAIProvider) do(ctx context.Context, path string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+path, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Try to surface the structured error message before falling back.
		var e openAIResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			return nil, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, e.Error.Message)
		}
		return nil, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, snippet(raw, 200))
	}
	return raw, nil
}

func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// openAIUserContent shapes the user-turn content. With no images, returns
// the plain text string (compact wire format). With images, returns a parts
// slice so the model sees both the prompt text and each image.
func openAIUserContent(r Request) (any, error) {
	if len(r.Images) == 0 {
		return r.UserMessage, nil
	}
	parts := make([]openAIContentPart, 0, 1+len(r.Images))
	if r.UserMessage != "" {
		parts = append(parts, openAIContentPart{Type: "text", Text: r.UserMessage})
	}
	for _, img := range r.Images {
		if len(img.Data) == 0 || img.MIMEType == "" {
			return nil, fmt.Errorf("openai: image attachment missing data or mime type")
		}
		dataURL := "data:" + img.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		parts = append(parts, openAIContentPart{
			Type:     "image_url",
			ImageURL: &openAIImageURLRef{URL: dataURL},
		})
	}
	return parts, nil
}
