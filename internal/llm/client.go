// Package llm provides an LLM client for task planning.
//
// The client is compatible with OpenAI's API and can work with any
// provider that implements the same interface (OpenAI, Azure OpenAI,
// local models via Ollama, etc.)
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// DefaultTimeout is the default timeout for LLM requests.
const DefaultTimeout = 60 * time.Second

// Client is an OpenAI-compatible LLM client.
type Client struct {
	endpoint   string
	model      string
	apiKey     string
	httpClient *http.Client
}

// Config configures the LLM client.
type Config struct {
	Endpoint string        // API endpoint (e.g., "https://api.openai.com/v1/chat/completions")
	Model    string        // Model name (e.g., "gpt-4o", "claude-3-sonnet")
	APIKey   string        // API key (can be empty string for local models)
	Timeout  time.Duration // Request timeout (default: 60s)
}

// New creates a new LLM client.
func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	// Allow env var override for API key
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("ORCHESTRA_LLM_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	return &Client{
		endpoint: cfg.Endpoint,
		model:    cfg.Model,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// ChatMessage represents a message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"` // Message content
}

// ChatRequest is the request body for chat completions.
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat specifies the output format.
type ResponseFormat struct {
	Type       string          `json:"type"`                  // "json_object" or "json_schema"
	JSONSchema *JSONSchemaSpec `json:"json_schema,omitempty"` // For structured output
}

// JSONSchemaSpec defines a JSON schema for structured output.
type JSONSchemaSpec struct {
	Name   string      `json:"name"`
	Strict bool        `json:"strict"`
	Schema interface{} `json:"schema"`
}

// ChatResponse is the response from chat completions.
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a single completion choice.
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Complete sends a chat completion request and returns the response.
func (c *Client) Complete(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	return c.completeWithFormat(ctx, messages, nil)
}

// CompleteJSON sends a request with JSON output format.
// The response is guaranteed to be valid JSON.
func (c *Client) CompleteJSON(ctx context.Context, messages []ChatMessage) (json.RawMessage, error) {
	format := &ResponseFormat{
		Type: "json_object",
	}

	resp, err := c.completeWithFormat(ctx, messages, format)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no completions returned")
	}

	return json.RawMessage(resp.Choices[0].Message.Content), nil
}

// CompleteStructured sends a request with a JSON schema for structured output.
// The response will conform to the provided schema.
func (c *Client) CompleteStructured(ctx context.Context, messages []ChatMessage, schemaName string, schema interface{}) (json.RawMessage, error) {
	format := &ResponseFormat{
		Type: "json_schema",
		JSONSchema: &JSONSchemaSpec{
			Name:   schemaName,
			Strict: true,
			Schema: schema,
		},
	}

	resp, err := c.completeWithFormat(ctx, messages, format)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no completions returned")
	}

	return json.RawMessage(resp.Choices[0].Message.Content), nil
}

// completeWithFormat sends a chat completion request with optional format.
func (c *Client) completeWithFormat(ctx context.Context, messages []ChatMessage, format *ResponseFormat) (*ChatResponse, error) {
	req := ChatRequest{
		Model:          c.model,
		Messages:       messages,
		Temperature:    0.2, // Low temperature for more deterministic planning
		ResponseFormat: format,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", httpResp.StatusCode, string(respBody))
	}

	var resp ChatResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// GetContent extracts the content from the first choice in a response.
func (c *Client) GetContent(resp *ChatResponse) (string, error) {
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no completions returned")
	}
	return resp.Choices[0].Message.Content, nil
}
