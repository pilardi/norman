// Package llm provides an OpenAI-compatible API client for the Norman agent.
// It wraps HTTP calls to a configurable LLM endpoint (e.g., GPT-4o, Claude, local models).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/norman/norman/pkg/config"
)

// Message represents a single chat message in the conversation.
type Message struct {
	Role    string `json:"role"`     // "system", "user", "assistant", "tool"
	Content string `json:"content"`  // The message content
	Name    string `json:"name,omitempty"` // For tool messages: the tool name
	ID      string `json:"id,omitempty"`   // Tool call ID (for response)
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ResponseChoice represents a single choice from the API response.
type ResponseChoice struct {
	Message    Message   `json:"message"`
	FinishReason string  `json:"finish_reason"`
	Index      int       `json:"index"`
}

// Usage tracks token consumption for a completion call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// CompletionResponse is the full API response from the LLM endpoint.
type CompletionResponse struct {
	ID      string        `json:"id"`
	Model   string        `json:"model"`
	Choices []ResponseChoice `json:"choices"`
	Usage   Usage         `json:"usage"`
}

// Caller is the interface that LLM clients must implement for the agent loop.
type Caller interface {
	Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error)
}

// Client wraps an OpenAI-compatible chat completions API call.
type Client struct {
	BaseURL    string
	APIKey     string
	Model      string
	Temperature  float64
	MaxTokens  int
	HTTPClient *http.Client
	Timeout    time.Duration
}

// NewClient creates a client configured from the system config.
func NewClient(cfg *config.SystemConfig) *Client {
	return &Client{
		BaseURL:   cfg.OpenShell.Address, // resolved gateway; for MVP, this is the endpoint base
		Model:     cfg.DefaultModel,
		Temperature: cfg.Temperature,
		MaxTokens: cfg.MaxTokens,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
}

// NewClientWithConfig creates a client from a specific workspace config.
func NewClientWithConfig(cfg *config.WorkspaceConfig) *Client {
	return &Client{
		Model:      cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:  cfg.MaxTokens,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
}

// Complete sends a chat completion request and returns the response.
// It supports multi-turn conversations via a messages array.
func (c *Client) Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: c.Timeout}
	}

	// Build the request body
	requestBody := map[string]interface{}{
		"model":     c.Model,
		"temperature": c.Temperature,
		"max_tokens":  c.MaxTokens,
		"messages":    append([]map[string]string{{"role": "system", "content": systemPrompt}}, messagesToMap(messages)...),
	}

	if len(tools) > 0 {
		requestBody["tools"] = toolsToJSON(tools)
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("llm: HTTP %d, body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var completion CompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return &completion, nil
}

// messagesToMap converts a slice of Message to a slice of map[string]string.
func messagesToMap(msgs []Message) []map[string]string {
	result := make([]map[string]string, len(msgs))
	for i, m := range msgs {
		result[i] = map[string]string{
			"role":    m.Role,
			"content": m.Content,
		}
		if m.Name != "" {
			result[i]["name"] = m.Name
		}
		if m.ID != "" {
			result[i]["id"] = m.ID
		}
	}
	return result
}

// toolsToJSON converts a slice of Tool to JSON array.
func toolsToJSON(tools []Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		result[i] = map[string]interface{}{
			"type":        "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		}
	}
	return result
}

// Tool defines a callable tool that the LLM can invoke.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ParseToolCalls extracts tool calls from a completion response's message.
func ParseToolCalls(msg Message) []ToolCall {
	// For MVP, we parse the content as JSON if it looks like a tool call
	// In a real implementation, this would check for structured tool_call fields
	var calls []ToolCall

	if msg.Role == "assistant" && len(msg.Content) > 0 {
		// Try to parse as a tool call JSON object
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Content), &raw); err == nil {
			if name, ok := raw["name"].(string); ok {
				calls = append(calls, ToolCall{
					ID:      fmt.Sprintf("call_%d", time.Now().UnixNano()),
					Name:    name,
					Arguments: []byte(fmt.Sprintf("%v", raw)),
				})
			}
		}
	}

	return calls
}

// ParseResponseText extracts the text content from a completion response.
func ParseResponseText(resp *CompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

// ParseUsage returns token usage from a completion response.
func ParseUsage(resp *CompletionResponse) Usage {
	return resp.Usage
}
