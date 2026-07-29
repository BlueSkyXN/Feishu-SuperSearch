package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type ClientConfig struct {
	BaseURL          string
	APIKey           string
	Timeout          time.Duration
	MaxInputBytes    int64
	MaxResponseBytes int64
	JSONSchema       bool
	HTTPClient       *http.Client
}

type Client struct {
	baseURL          string
	apiKey           string
	httpClient       *http.Client
	maxInputBytes    int64
	maxResponseBytes int64
	jsonSchema       bool
}

func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("AI base URL is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrIdentityRequired, Message: "AI API key is not configured"}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.MaxInputBytes <= 0 {
		cfg.MaxInputBytes = 4 << 20
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 4 << 20
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{baseURL: baseURL, apiKey: strings.TrimSpace(cfg.APIKey), httpClient: httpClient, maxInputBytes: cfg.MaxInputBytes, maxResponseBytes: cfg.MaxResponseBytes, jsonSchema: cfg.JSONSchema}, nil
}

type completionRequest struct {
	Model          string         `json:"model"`
	Messages       []message      `json:"messages"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *Client) CompleteJSON(ctx context.Context, model, system, user, schemaName string, schema map[string]any, maxOutputTokens int) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("AI client is nil")
	}
	if strings.TrimSpace(model) == "" {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "AI model is required"}
	}
	reqBody := completionRequest{Model: model, Messages: []message{{Role: "system", Content: system}, {Role: "user", Content: user}}, Temperature: 0, MaxTokens: maxOutputTokens}
	if c.jsonSchema && len(schema) > 0 {
		reqBody.ResponseFormat = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": schemaName, "strict": true, "schema": schema}}
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > c.maxInputBytes {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "AI request exceeds max_input_bytes"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrDeadlineExceeded, Message: "AI request timed out", Retryable: true}
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, &kernel.ErrorDetail{Type: kernel.ErrCancelled, Message: "AI request cancelled"}
		}
		return nil, &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Message: "AI request failed: " + err.Error(), Retryable: true}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, c.maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxResponseBytes {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrBudgetExhausted, Message: "AI response exceeds max_response_bytes"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, mapHTTPError(resp.StatusCode, body)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Choices) == 0 {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "AI response has no choices"}
	}
	content := extractContent(envelope.Choices[0].Message.Content)
	content = stripCodeFence(strings.TrimSpace(content))
	if content == "" {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "AI response content is empty"}
	}
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "AI response is not valid JSON: " + err.Error()}
	}
	return []byte(content), nil
}

func extractContent(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var parts []string
		for _, item := range content {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func stripCodeFence(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSpace(content)
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}

func mapHTTPError(status int, body []byte) error {
	message := http.StatusText(status)
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		message = envelope.Error.Message
	}
	detail := &kernel.ErrorDetail{Message: "AI upstream: " + message}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		detail.Type = kernel.ErrIdentityRequired
	case status == http.StatusTooManyRequests:
		detail.Type = kernel.ErrRateLimited
		detail.Retryable = true
	case status >= 500:
		detail.Type = kernel.ErrUpstreamTransient
		detail.Retryable = true
	default:
		detail.Type = kernel.ErrUpstreamPermanent
	}
	return detail
}
