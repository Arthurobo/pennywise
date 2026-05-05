package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GeminiBaseURL is the default REST root for the v1beta API.
const GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiProvider wraps /models/{model}:generateContent.
type GeminiProvider struct {
	APIKey  string
	BaseURL string // empty → GeminiBaseURL
	Client  *http.Client
}

func (p *GeminiProvider) Name() string { return "gemini" }

func (p *GeminiProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return GeminiBaseURL
}

func (p *GeminiProvider) httpClient() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// geminiPart can carry either text or inline binary data (base64-encoded
// image). Both fields are omitempty so each part is exactly one or the
// other in the wire payload.
type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	Temperature      float64 `json:"temperature,omitempty"`
	MaxOutputTokens  int     `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent          `json:"contents"`
	SystemInstruction *geminiSystemInstruction `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (p *GeminiProvider) Complete(ctx context.Context, r Request) (Response, error) {
	parts, err := geminiUserParts(r)
	if err != nil {
		return Response{}, err
	}
	body := geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: parts},
		},
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     r.Temperature,
			MaxOutputTokens: r.MaxTokens,
		},
	}
	if r.SystemPrompt != "" {
		body.SystemInstruction = &geminiSystemInstruction{
			Parts: []geminiPart{{Text: r.SystemPrompt}},
		}
	}
	if r.JSONMode {
		body.GenerationConfig.ResponseMimeType = "application/json"
	}

	path := fmt.Sprintf("/models/%s:generateContent", r.Model)
	raw, err := p.do(ctx, path, body)
	if err != nil {
		return Response{}, err
	}
	var resp geminiResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("gemini: decode response: %w", err)
	}
	if resp.Error != nil {
		return Response{}, fmt.Errorf("gemini: %s", resp.Error.Message)
	}
	if len(resp.Candidates) == 0 {
		return Response{}, fmt.Errorf("gemini: no candidates")
	}
	var text strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}
	return Response{
		Text:         text.String(),
		InputTokens:  resp.UsageMetadata.PromptTokenCount,
		OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
	}, nil
}

func (p *GeminiProvider) Test(ctx context.Context, model string) error {
	if model == "" {
		model = DefaultModel("gemini")
	}
	_, err := p.Complete(ctx, Request{
		Model:        model,
		SystemPrompt: "You are a connectivity test. Reply with exactly: OK",
		UserMessage:  "ping",
		Temperature:  0,
		MaxTokens:    16,
	})
	return err
}

// geminiUserParts builds the user-turn `parts` slice. Text-only requests
// produce a single text part; image-bearing requests append one inline_data
// part per attachment.
func geminiUserParts(r Request) ([]geminiPart, error) {
	parts := make([]geminiPart, 0, 1+len(r.Images))
	if r.UserMessage != "" {
		parts = append(parts, geminiPart{Text: r.UserMessage})
	}
	for _, img := range r.Images {
		if len(img.Data) == 0 || img.MIMEType == "" {
			return nil, fmt.Errorf("gemini: image attachment missing data or mime type")
		}
		parts = append(parts, geminiPart{
			InlineData: &geminiInlineData{
				MIMEType: img.MIMEType,
				Data:     base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	if len(parts) == 0 {
		// Gemini rejects empty parts. Fallback: a single empty text part
		// so the prompt is system-only, matching the old behavior.
		parts = append(parts, geminiPart{Text: ""})
	}
	return parts, nil
}

func (p *GeminiProvider) do(ctx context.Context, path string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: encode: %w", err)
	}
	// Gemini accepts the API key as a query parameter; the alternative is the
	// `x-goog-api-key` header. Query param keeps the surface uniform with the
	// other providers.
	url := p.baseURL() + path + "?key=" + p.APIKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("gemini: build: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e geminiResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			return nil, fmt.Errorf("gemini HTTP %d: %s", resp.StatusCode, e.Error.Message)
		}
		return nil, fmt.Errorf("gemini HTTP %d: %s", resp.StatusCode, snippet(raw, 200))
	}
	return raw, nil
}
