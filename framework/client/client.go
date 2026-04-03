// Package client provides HTTP clients for LLM inference APIs (OpenAI-compatible).
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LLMClient talks to an OpenAI-compatible LLM inference endpoint.
type LLMClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New creates an LLMClient. It skips TLS verification since vLLM uses self-signed certs with mTLS.
func New(baseURL string) *LLMClient {
	return &LLMClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // G402: intentional skip for self-signed vLLM TLS certs
			},
		},
	}
}

// HealthCheck hits the /health endpoint and returns nil if healthy.
func (c *LLMClient) HealthCheck(ctx context.Context) error {
	url := c.BaseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating health request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("health check returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// CompletionRequest is the /v1/completions request body.
type CompletionRequest struct {
	Model     string  `json:"model"`
	Prompt    string  `json:"prompt"`
	MaxTokens int     `json:"max_tokens,omitempty"`
	Temp      float64 `json:"temperature,omitempty"`
}

// CompletionResponse is the /v1/completions response body.
type CompletionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   Usage              `json:"usage"`
}

// CompletionChoice represents a single completion choice.
type CompletionChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
}

// ChatRequest is the /v1/chat/completions request body.
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Temp      float64       `json:"temperature,omitempty"`
}

// ChatMessage represents a message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the /v1/chat/completions response body.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

// ChatChoice represents a single chat completion choice.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage captures token usage from the API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelInfo represents a model from /v1/models.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsResponse is the /v1/models response body.
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// Completions sends a completion request and returns the response.
func (c *LLMClient) Completions(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = 50
	}
	var resp CompletionResponse
	if err := c.post(ctx, "/v1/completions", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ChatCompletions sends a chat completion request and returns the response.
func (c *LLMClient) ChatCompletions(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = 50
	}
	var resp ChatResponse
	if err := c.post(ctx, "/v1/chat/completions", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListModels calls /v1/models and returns available models.
func (c *LLMClient) ListModels(ctx context.Context) (*ModelsResponse, error) {
	url := c.BaseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models returned %d: %s", resp.StatusCode, string(body))
	}

	var models ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}
	return &models, nil
}

func (c *LLMClient) post(ctx context.Context, path string, reqBody, respBody interface{}) error {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, respBody); err != nil {
		return fmt.Errorf("decoding response from %s: %w", path, err)
	}
	return nil
}
