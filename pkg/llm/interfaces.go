// Package llm provides provider-agnostic LLM and embedding interfaces for the Norman agent.
package llm

import (
	"context"
	"encoding/json"
)

// Message represents a single chat message in the conversation.
type Message struct {
	Role    string `json:"role"`     // "system", "user", "assistant", "tool"
	Content string `json:"content"`  // The message content
	Name    string `json:"name,omitempty"` // For tool messages: the tool name
	ID      string `json:"id,omitempty"`   // Tool call ID (for response)
}

// Tool represents a callable tool that the LLM can invoke.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
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
}

// ResponseChoice is kept for backward compatibility with existing code that accesses resp.Choices[0].Message.
// New code should prefer resp.Message directly.
type ResponseChoice struct {
	Message      Message   `json:"message"`
	FinishReason string  `json:"finish_reason"`
	Index        int       `json:"index"`
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
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
