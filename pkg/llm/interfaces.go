// Package llm provides provider-agnostic LLM and embedding interfaces for the Norman agent.
package llm

import "github.com/norman/norman/pkg/llm/api"

// Re-exported types from api for backward compatibility.
type Message = api.Message
type Tool = api.Tool
type CompletionRequest = api.CompletionRequest
type CompletionResponse = api.CompletionResponse
type Usage = api.Usage
type LLM = api.LLM
type Embedder = api.Embedder
type StreamReader = api.StreamReader
type Chunk = api.Chunk
type ResponseChoice = api.ResponseChoice
type ToolCall = api.ToolCall
type ProviderMetadata = api.ProviderMetadata
type Caller = api.Caller
