// Package llm provides an OpenAI-compatible API client for the Norman agent.
// It implements the LLM interface and handles typed error mapping from provider responses.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/llm/internal/sse"
	"github.com/norman/norman/pkg/protocol"
)

// Client implements the LLM interface for OpenAI-compatible endpoints.
type Client struct {
	BaseURL      string
	APIKey       string
	Model        string
	Temperature  float64
	MaxTokens    int
	ProviderName string // configured provider name (e.g. "local-llamacpp", "unsloth")
	path         string // full API path for this provider type (e.g. "/v1/chat/completions" or "/completions")
	HTTPClient   *http.Client
	Timeout      time.Duration
}

// NewClient creates a client configured from the system config.
// Deprecated: use NewClientWithProvider for provider-specific configuration.
func NewClient(cfg *config.SystemConfig) *Client {
	return &Client{
		BaseURL:     cfg.OpenShell.Address, // resolved gateway; for MVP, this is the endpoint base
		Model:       "",                    // empty = no default model (use provider config instead)
		Temperature: 0,                     // zero = use provider default
		MaxTokens:   0,                     // zero = use provider default
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
}

// NewClientWithProvider creates a client configured from a specific provider config.
func NewClientWithProvider(provCfg *config.ProviderConfig) *Client {
	var path string
	switch provCfg.Type {
	case "llamacpp":
		path = "/completions"
	default: // openai and others
		path = "/v1/chat/completions"
	}
	return &Client{
		BaseURL:      provCfg.Endpoint,
		Model:        provCfg.Model,
		Temperature:  provCfg.Temperature,
		MaxTokens:    provCfg.MaxTokens,
		APIKey:       provCfg.ApiKey,
		ProviderName: provCfg.Name,
		path:         path,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
}

// NewClientWithConfig creates a client from a specific workspace config.
func NewClientWithConfig(cfg *config.WorkspaceConfig) *Client {
	return &Client{
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
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

	// Use client's default model if not set in the request.
	if req.Model == "" && c.Model != "" {
		req.Model = c.Model
	}
	requestBody := buildRequestBody(req)
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	baseURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("parse base URL: %w", err))
	}
	urlPath := baseURL.JoinPath(c.path)

	reqHTTP, err := http.NewRequestWithContext(ctx, "POST", urlPath.String(), bytes.NewReader(payload))
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
	requestBody["stream_options"] = map[string]interface{}{
		"include_usage": true,
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	baseURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, wrapError(ErrProviderDown, fmt.Errorf("parse base URL: %w", err))
	}
	urlPath := baseURL.JoinPath(c.path)

	reqHTTP, err := http.NewRequestWithContext(ctx, "POST", urlPath.String(), bytes.NewReader(payload))
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, wrapError(ErrProviderDown, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, string(body)))
	}

	return sse.NewStreamReader(resp, c.ProviderName), nil
}

// ProviderInfo returns metadata about the underlying provider.
func (c *Client) ProviderInfo() ProviderMetadata {
	name := c.ProviderName
	if name == "" {
		name = string(protocol.ProviderConfigTypeOpenai) // fallback to type constant "openai"
	}
	return ProviderMetadata{
		Name:             name,
		SupportsTools:    true,
		SupportsStream:   true,
		MaxTokensPerCall: c.MaxTokens,
		BatchSupport:     false, // OpenAI batch API is separate; not exposed here at MVP
	}
}

// Logf logs with a [llm][{provider}] prefix.
func (c *Client) Logf(format string, args ...any) {
	name := c.ProviderName
	if name == "" {
		name = string(protocol.ProviderConfigTypeOpenai)
	}
	log.Printf("[llm][%s] "+format, append([]any{name}, args...)...)
}

// --- Helpers ---

func buildRequestBody(req CompletionRequest) map[string]interface{} {
	body := map[string]interface{}{
		"model":       req.Model,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"messages":    messagesToJSON(req.Messages),
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

func messagesToJSON(msgs []Message) []map[string]any {
	result := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		msgMap := map[string]any{
			"role": m.Role,
		}

		// If ContentParts is provided, use it for the content array
		if len(m.ContentParts) > 0 {
			contentParts := make([]map[string]any, len(m.ContentParts))
			for j, cp := range m.ContentParts {
				cpMap := map[string]any{
					"type": cp.Type,
				}
				if cp.Text != "" {
					cpMap["text"] = cp.Text
				}
				if cp.CacheControl != nil {
					cpMap["cache_control"] = map[string]string{
						"type": cp.CacheControl.Type,
					}
				}
				contentParts[j] = cpMap
			}
			msgMap["content"] = contentParts
		} else {
			// Fallback to string content
			msgMap["content"] = m.Content
		}

		if m.Name != "" {
			msgMap["name"] = m.Name
		}
		if m.ID != "" {
			msgMap["id"] = m.ID
		}

		result[i] = msgMap
	}
	return result
}

func toolsToJSON(tools []Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		result[i] = map[string]interface{}{
			"type": "function",
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
		return wrapError(ErrProviderDown, fmt.Errorf("%s", body))

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

// QueryProviderModels fetches available models for the given provider config.
func QueryProviderModels(provCfg *config.ProviderConfig) ([]protocol.AvailableModelEntry, error) {
	var entries []protocol.AvailableModelEntry
	switch strings.ToLower(provCfg.Type) {
	case "openai", "gemini":
		models, err := fetchOpenAIModels(provCfg.Endpoint, provCfg.ApiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models from %s: %w", provCfg.Endpoint, err)
		}
		entries = models
	case "ollama":
		models, err := fetchOllamaModels(provCfg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models from %s: %w", provCfg.Endpoint, err)
		}
		entries = models
	case "llamacpp":
		models, err := fetchOpenAIModels(provCfg.Endpoint, "")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models from %s: %w", provCfg.Endpoint, err)
		}
		entries = models
	default:
		return nil, fmt.Errorf("unsupported provider type %q for model discovery", provCfg.Type)
	}
	return entries, nil
}

// fetchOpenAIModels queries an OpenAI-compatible endpoint's /v1/models.
func fetchOpenAIModels(endpoint, apiKey string) ([]protocol.AvailableModelEntry, error) {
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	modelsURL := baseURL.JoinPath("/v1/models")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, modelsURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// OpenAI API returns {"object":"list","data":[...]}, not {"models":[...]}.
	var openaiResp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var entries []protocol.AvailableModelEntry
	for _, m := range openaiResp.Data {
		entry := protocol.AvailableModelEntry{}
		if id, ok := m["id"].(string); ok {
			idStr := id
			entry.Id = &idStr
		}
		if name, ok := m["name"].(string); ok {
			nameStr := name
			entry.Name = &nameStr
		}
		if ownedBy, ok := m["owned_by"].(string); ok {
			obStr := ownedBy
			entry.OwnedBy = &obStr
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// fetchOllamaModels queries an Ollama endpoint's /api/tags.
func fetchOllamaModels(endpoint string) ([]protocol.AvailableModelEntry, error) {
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	tagsURL := baseURL.JoinPath("/api/tags")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tagsURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var entries []protocol.AvailableModelEntry
	for _, m := range ollamaResp.Models {
		entry := protocol.AvailableModelEntry{}
		if name, ok := m["name"].(string); ok {
			n := name
			entry.Id = &n
			nameStr := n
			entry.Name = &nameStr
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
