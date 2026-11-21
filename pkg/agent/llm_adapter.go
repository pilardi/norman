package agent

import (
	"context"
	"fmt"

	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/llm"
)

// llmClientAdapter adapts llm.LLM to the budget.LLMClient interface.
type llmClientAdapter struct {
	client llm.LLM
}

// Complete implements the budget.LLMClient interface by converting the prompt to a CompletionRequest.
func (a *llmClientAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		Model:       "", // Will use the client's default model
		Temperature: 0.1,
		MaxTokens:   1000,
	}

	resp, err := a.client.Complete(ctx, req)
	if err != nil {
		return "", err
	}

	if resp == nil {
		return "", fmt.Errorf("no response from LLM")
	}

	return resp.Message.Content, nil
}

// NewLLMClientAdapter creates a new LLM client adapter for the budget compressor.
func NewLLMClientAdapter(client llm.LLM) budget.LLMClient {
	return &llmClientAdapter{client: client}
}
