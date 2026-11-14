// Package api defines the shared types used across LLM providers.
// Both the llm package and its internal implementations import this package
// to avoid circular dependencies.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Message represents a single chat message in the conversation.
type Message struct {
	Role    string `json:"role"`           // "system", "user", "assistant", "tool"
	Content string `json:"content"`        // The message content
	Name    string `json:"name,omitempty"` // For tool messages: the tool name
	ID      string `json:"id,omitempty"`   // Tool call ID (for response)
}

// Schema represents a JSON Schema node for tool parameters.
// Using a typed struct instead of map[string]interface{} ensures
// consistent serialization and proper type handling for llama-server compatibility.
type Schema struct {
	Type        string             `json:"type"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Description string             `json:"description,omitempty"`
}

// Tool represents a callable tool that the LLM can invoke.
type Tool struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Parameters  *Schema `json:"parameters,omitempty"`
}

// CompletionRequest is the universal request shape for all providers.
type CompletionRequest struct {
	Messages         []Message
	Tools            []Tool
	Model            string
	Temperature      float64
	MaxTokens        int
	StopSequences    []string
	PresencePenalty  float32
	FrequencyPenalty float32
}

// CompletionResponse is the universal response shape.
// It supports both flat (Message) and legacy dual-structure (Choices) for backward compatibility.
type CompletionResponse struct {
	ID           string
	Model        string
	Message      Message
	FinishReason string
	Usage        Usage
	RawJSON      []byte

	// Choices is kept for backward compatibility with existing code that accesses resp.Choices[0].Message.
	// New code should prefer resp.Message directly.
	Choices []ResponseChoice `json:"choices,omitempty"`
}

// Usage tracks token consumption for a completion call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// LLM provides text completion with optional tool-calling support.
type LLM interface {
	// Complete sends a chat completion request and returns the full response.
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)

	// Stream sends a chat completion request and yields responses incrementally.
	Stream(ctx context.Context, req CompletionRequest) (StreamReader, error)

	// ProviderInfo returns metadata about the underlying provider.
	ProviderInfo() ProviderMetadata
}

// Embedder provides vector embedding for text content.
type Embedder interface {
	// EmbedText returns a float32 vector for the given text.
	EmbedText(ctx context.Context, text string) ([]float32, error)
}

// StreamReader yields incremental chunks from a streaming response.
type StreamReader interface {
	Next() (Chunk, error)
	Close() error
}

// Chunk represents a single increment from a streaming response.
type Chunk struct {
	Delta        string
	FinishReason string
	Usage        *Usage
	ToolCalls    []ToolCall // Captured tool calls from delta.tool_calls field
}

// ResponseChoice is kept for backward compatibility with existing code that accesses resp.Choices[0].Message.
// New code should prefer resp.Message directly.
type ResponseChoice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
	Index        int     `json:"index"`
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"` // raw JSON bytes (backward compat)
	// ArgumentsTyped provides parsed typed access to arguments when available.
	// Populated via ParseArguments() for convenience. Nil if unparseable.
	ArgumentsTyped map[string]interface{} `json:"-"`
}

// ParseArguments deserializes the raw Arguments into a typed map.
// Returns nil if Arguments is empty or invalid JSON.
func (tc *ToolCall) ParseArguments() map[string]interface{} {
	if len(tc.Arguments) == 0 {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(tc.Arguments, &m); err != nil {
		return nil
	}
	return m
}

// ProviderMetadata describes the provider's capabilities and constraints.
type ProviderMetadata struct {
	Name             string // "openai", "anthropic", "ollama", "gemini"
	SupportsTools    bool
	SupportsStream   bool
	MaxTokensPerCall int
	BatchSupport     bool
}

// Caller is the legacy MVP interface — preserved for backward compatibility.
// Implemented by ClientAdapter wrapping any LLM implementation.
type Caller interface {
	Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error)
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

// ToolCallAccum accumulates streamed arguments for a single tool call.
// Used during SSE streaming to concatenate partial argument chunks across multiple deltas.
type ToolCallAccum struct {
	Name string
	Args []byte
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
