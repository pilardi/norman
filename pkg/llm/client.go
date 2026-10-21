// Package llm provides an OpenAI-compatible API client for the Norman agent.
// It implements the LLM interface and handles typed error mapping from provider responses.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
)

// Client implements the LLM interface for OpenAI-compatible endpoints.
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

// Complete sends a chat completion request and returns the full response.
func (c *Client) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: c.Timeout}
	}

	requestBody := buildRequestBody(req)
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"

	reqHTTP, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("create request: %w", err))
	}

	reqHTTP.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		reqHTTP.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(reqHTTP)
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("HTTP request failed: %w", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("read response body: %w", err))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, mapHTTPError(resp.StatusCode, string(body))
	}

	var completion CompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("parse response: %w", err))
	}

	if len(completion.Message.Content) == 0 && len(completion.Choices) == 0 {
		// Fallback: try parsing the flat Message field
		completion.Choices = []ResponseChoice{{Message: completion.Message, FinishReason: completion.FinishReason}}
	}

	return &completion, nil
}

// Stream sends a chat completion request and yields responses incrementally via SSE.
func (c *Client) Stream(ctx context.Context, req CompletionRequest) (StreamReader, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: c.Timeout}
	}

	requestBody := buildRequestBody(req)
	requestBody["stream"] = true
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"

	reqHTTP, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("create request: %w", err))
	}

	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		reqHTTP.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(reqHTTP)
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("HTTP request failed: %w", err))
	}

	return &sseStreamReader{
		resp: resp,
		done: make(chan struct{}),
	}, nil
}

// ProviderInfo returns metadata about the underlying provider.
func (c *Client) ProviderInfo() ProviderMetadata {
	return ProviderMetadata{
		Name:           ProviderOpenAI,
		SupportsTools:  true,
		SupportsStream: true,
		MaxTokensPerCall: c.MaxTokens,
		BatchSupport:   false, // OpenAI batch API is separate; not exposed here at MVP
	}
}

// --- SSE Streaming Reader ---

type sseStreamReader struct {
	resp *http.Response
	done chan struct{}
	buf  []byte
}

func (s *sseStreamReader) Next() (Chunk, error) {
	for {
		line, err := readSSELine(s.resp.Body)
		if err != nil {
			return Chunk{}, fmt.Errorf("read SSE: %w", err)
		}
		if line == "" {
			continue // empty line between events
		}

		if line == "data: [DONE]" {
			return Chunk{FinishReason: "[done]"}, nil
		}

		if !bytes.HasPrefix([]byte(line), []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix([]byte(line), []byte("data: "))
		var delta map[string]interface{}
		if err := json.Unmarshal(data, &delta); err != nil {
			return Chunk{}, fmt.Errorf("unmarshal SSE data: %w", err)
		}

		chunk := Chunk{}
		if msg, ok := delta["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				chunk.Delta = content
			}
		}
		if fr, ok := delta["finish_reason"].(string); ok {
			chunk.FinishReason = fr
		}

		return chunk, nil
	}
}

func (s *sseStreamReader) Close() error {
	s.resp.Body.Close()
	close(s.done)
	return nil
}

// readSSELine reads a single SSE event line from the response body.
func readSSELine(r io.Reader) (string, error) {
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if err != nil {
		return "", err
	}
	// Trim trailing newline(s) that separate SSE events
	return string(bytes.TrimSpace(buf[:n])), nil
}

// --- Helpers ---

func buildRequestBody(req CompletionRequest) map[string]interface{} {
	body := map[string]interface{}{
		"model":     req.Model,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"messages":    messagesToMap(req.Messages),
	}

	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	if req.PresencePenalty != 0 {
		body["presence_penalty"] = req.PresencePenalty
	}
	if req.FrequencyPenalty != 0 {
		body["frequency_penalty"] = req.FrequencyPenalty
	}

	if len(req.Tools) > 0 {
		body["tools"] = toolsToJSON(req.Tools)
	}

	return body
}

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

// --- Error Mapping ---

// mapHTTPError maps HTTP status codes and response bodies to typed LLM errors.
func mapHTTPError(statusCode int, body string) error {
	switch statusCode {
	case http.StatusTooManyRequests:
		return ErrRateLimit
	case http.StatusUnprocessableEntity:
		// Common for context-window exceeded (OpenAI returns 422 with a message)
		if containsSubstring(body, "context window") || containsSubstring(body, "max_tokens") {
			return ErrContextWindow
		}
		return wrapError(ErrProviderDown, fmt.Errorf("HTTP %d: %s", statusCode, body))

	case http.StatusInternalServerError:
		fallthrough
	case http.StatusBadGateway:
		fallthrough
	case http.StatusServiceUnavailable:
		return ErrProviderDown

	default:
		return fmt.Errorf("HTTP %d: %s", statusCode, body)
	}
}

// wrapError wraps an error with typed LLMError metadata.
func wrapError(typedErr *LLMError, original error) *LLMError {
	wrapped := &LLMError{
		Code:     typedErr.Code,
		Message:  typedErr.Message,
		Original: original,
	}
	return wrapped
}

// containsSubstring is a case-insensitive substring check.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (len(s) == 0 || len(substr) == 0 ||
		bytes.Contains([]byte(strings.ToLower(s)), []byte(strings.ToLower(substr))))
}

// ParseToolCalls extracts tool calls from a completion response's message.
// Supports both OpenAI-style structured tool_calls array and legacy JSON-in-content format.
func ParseToolCalls(msg Message) []ToolCall {
	var calls []ToolCall

	if msg.Role != "assistant" || len(msg.Content) == 0 {
		return calls
	}

	// Strategy 1: Check for structured tool_calls field in message (OpenAI API response).
	var rawMsg map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &rawMsg); err == nil {
		if toolCalls, ok := rawMsg["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
			for _, tcRaw := range toolCalls {
				tcMap, ok := tcRaw.(map[string]interface{})
				if !ok {
					continue
				}
				id, _ := tcMap["id"].(string)
				fnRaw, _ := tcMap["function"].(map[string]interface{})
				name, _ := fnRaw["name"].(string)
				argsRaw, _ := fnRaw["arguments"].(string)

				tc := ToolCall{
					ID:        id,
					Name:      name,
					Arguments: []byte(argsRaw),
				}
				if argsRaw != "" {
					var m map[string]interface{}
					if err := json.Unmarshal([]byte(argsRaw), &m); err == nil {
						tc.ArgumentsTyped = m
					}
				}
				calls = append(calls, tc)
			}
			return calls // Structured format found — no need for fallback
		}
	}

	// Strategy 2: Legacy JSON-in-content (MVP heuristic).
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &raw); err == nil {
		if name, ok := raw["name"].(string); ok {
			tc := ToolCall{
				ID:        fmt.Sprintf("call_%d", time.Now().UnixNano()),
				Name:      name,
				Arguments: []byte(fmt.Sprintf("%v", raw)),
			}
			tc.ArgumentsTyped = raw
			calls = append(calls, tc)
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
