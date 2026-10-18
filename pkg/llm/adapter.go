// Package llm provides a compatibility adapter wrapping any LLM implementation
// to satisfy the legacy Caller interface.
package llm

import "context"

// ClientAdapter wraps an LLM implementation to satisfy the legacy Caller interface.
// This enables zero-breaking changes for pkg/agent/loop.go during migration:
// the Loop continues to accept llm.Caller; callers simply instantiate
// ClientAdapter{llm: NewOpenAILLM(...)} or ClientAdapter{llm: NewOllamaLLM(...)}.
type ClientAdapter struct {
	llm LLM
}

// Complete satisfies the LLM interface by wrapping an LLM implementation.
func (a *ClientAdapter) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	resp, err := a.llm.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Preserve Choices for backward compatibility with existing code.
	return &CompletionResponse{
		ID:           resp.ID,
		Model:        resp.Model,
		Choices:      []ResponseChoice{{Message: resp.Message, FinishReason: resp.FinishReason}},
		Usage:        resp.Usage,
	}, nil
}

// LegacyComplete satisfies the legacy Caller interface.
// Deprecated: prefer Complete(ctx, CompletionRequest) or Stream().
func (a *ClientAdapter) LegacyComplete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error) {
	req := CompletionRequest{
		Messages: append([]Message{{Role: "system", Content: systemPrompt}}, messages...),
		Tools:    tools,
	}

	resp, err := a.llm.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Preserve existing field names for backward compatibility.
	// The legacy CompletionResponse uses Choices ([]ResponseChoice);
	// we synthesize a single-choice response from the new flat Message field.
	return &CompletionResponse{
		ID:           resp.ID,
		Model:        resp.Model,
		Choices:      []ResponseChoice{{Message: resp.Message, FinishReason: resp.FinishReason}},
		Usage:        resp.Usage,
	}, nil
}

// Stream delegates to the wrapped LLM implementation for streaming responses.

// Stream delegates to the wrapped LLM implementation for streaming responses.
func (a *ClientAdapter) Stream(ctx context.Context, req CompletionRequest) (StreamReader, error) {
	return a.llm.Stream(ctx, req)
}

// ProviderInfo delegates to the wrapped LLM implementation.
func (a *ClientAdapter) ProviderInfo() ProviderMetadata {
	return a.llm.ProviderInfo()
}

// NewClientAdapter creates a ClientAdapter wrapping the given LLM.
func NewClientAdapter(llm LLM) *ClientAdapter {
	return &ClientAdapter{llm: llm}
}
