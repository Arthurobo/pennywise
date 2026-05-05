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

// AnthropicBaseURL is the default REST root.
const AnthropicBaseURL = "https://api.anthropic.com/v1"

// AnthropicAPIVersion is the value sent in the anthropic-version header.
// Pin a known-good version; bump in releases when Anthropic ships changes.
const AnthropicAPIVersion = "2023-06-01"

// AnthropicProvider wraps the /messages endpoint.
//
// Anthropic doesn't have a "JSON mode" flag. To get reliable JSON we use the
// well-documented assistant-prefill technique: append an empty assistant turn
// whose content starts with `{`, which forces the model's continuation to
// stay inside the JSON object. We then prepend the `{` back to the response
// before handing it to the defensive parser.
type AnthropicProvider struct {
	APIKey  string
	BaseURL string // empty → AnthropicBaseURL
	Client  *http.Client
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) baseURL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return AnthropicBaseURL
}

func (p *AnthropicProvider) httpClient() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// anthropicMessage.Content is either a plain string (text-only) or a slice
// of anthropicContentBlock (mixed text + image). The Messages API accepts
// both shapes; using `any` keeps text-only requests on the compact form.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicContentBlock struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`       // always "base64" for inline images
	MediaType string `json:"media_type"` // "image/jpeg" etc.
	Data      string `json:"data"`       // base64-encoded payload
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	StopReason string         `json:"stop_reason"`
	Error      *anthropicError `json:"error"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (p *AnthropicProvider) Complete(ctx context.Context, r Request) (Response, error) {
	maxTok := r.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	userContent, err := anthropicUserContent(r)
	if err != nil {
		return Response{}, err
	}
	body := anthropicRequest{
		Model:       r.Model,
		System:      r.SystemPrompt,
		MaxTokens:   maxTok,
		Temperature: r.Temperature,
		Messages: []anthropicMessage{
			{Role: "user", Content: userContent},
		},
	}
	if r.JSONMode {
		// Prefill the assistant turn with `{`. The model's continuation
		// then stays inside the JSON object. We re-prepend `{` to the
		// raw response below so the defensive parser sees a complete JSON.
		body.Messages = append(body.Messages, anthropicMessage{
			Role:    "assistant",
			Content: "{",
		})
	}

	raw, err := p.do(ctx, "/messages", body)
	if err != nil {
		return Response{}, err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}
	if resp.Error != nil {
		return Response{}, fmt.Errorf("anthropic: %s", resp.Error.Message)
	}
	if len(resp.Content) == 0 {
		return Response{}, fmt.Errorf("anthropic: empty content")
	}
	// Concatenate any text blocks (Anthropic returns content as an array of
	// blocks; for non-tool-use replies there's typically one text block).
	var text strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	out := text.String()
	if r.JSONMode {
		// Re-attach the prefill character so the parser sees `{...}`.
		out = "{" + out
	}
	return Response{
		Text:         out,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}, nil
}

func (p *AnthropicProvider) Test(ctx context.Context, model string) error {
	if model == "" {
		model = DefaultModel("anthropic")
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

// anthropicUserContent shapes the user turn — plain string for text-only
// requests, or a content-block array when images are attached. Anthropic
// places the image source inline (base64) rather than as a URL.
func anthropicUserContent(r Request) (any, error) {
	if len(r.Images) == 0 {
		return r.UserMessage, nil
	}
	blocks := make([]anthropicContentBlock, 0, 1+len(r.Images))
	for _, img := range r.Images {
		if len(img.Data) == 0 || img.MIMEType == "" {
			return nil, fmt.Errorf("anthropic: image attachment missing data or mime type")
		}
		blocks = append(blocks, anthropicContentBlock{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: img.MIMEType,
				Data:      base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	if r.UserMessage != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: r.UserMessage})
	}
	return blocks, nil
}

func (p *AnthropicProvider) do(ctx context.Context, path string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL()+path, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", AnthropicAPIVersion)

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e anthropicResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, e.Error.Message)
		}
		return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, snippet(raw, 200))
	}
	return raw, nil
}
