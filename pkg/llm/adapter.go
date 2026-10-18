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

// Complete satisfies the legacy Caller interface by wrapping an LLM implementation.
// It converts (systemPrompt, messages, tools) into a CompletionRequest and returns
// a legacy-style CompletionResponse with Choices for backward compatibility.
func (a *ClientAdapter) Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error) {
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

// NewClientAdapter creates a ClientAdapter wrapping the given LLM.
func NewClientAdapter(llm LLM) *ClientAdapter {
	return &ClientAdapter{llm: llm}
}
