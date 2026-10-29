package budget

import (
	"context"
	"fmt"
	"testing"
)

// mockLLMClient implements LLMClient for testing.
type mockLLMClient struct {
	response  string
	err       error
	callCount int
}

func (m *mockLLMClient) Complete(ctx context.Context, prompt string) (string, error) {
	m.callCount++
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func TestNewCompressor(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	mock := &mockLLMClient{response: "compressed summary"}
	c := NewCompressor(mock, cfg)
	if c.config.MaxTokens != 128000 {
		t.Error("expected config to be passed through")
	}
}

func TestSummarizeEmpty(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	state := NewSessionState(cfg)
	c := NewCompressor(&mockLLMClient{response: "summary"}, cfg)

	result, err := c.Summarize(state, false)
	if err != ErrNoTurns {
		t.Errorf("expected ErrNoTurns, got %v", err)
	}
	if result != nil {
		t.Error("expected nil result for empty state")
	}
}

func TestSummarizeNothingToCompress(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Role: "user", Content: "Hello", Tokens: 5, Pinned: true})

	mock := &mockLLMClient{response: "compressed summary"}
	c := NewCompressor(mock, cfg)

	result, err := c.Summarize(state, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CompressedCount != 0 {
		t.Errorf("expected 0 compressed, got %d", result.CompressedCount)
	}
	if result.KeptCount != 1 {
		t.Errorf("expected 1 kept, got %d", result.KeptCount)
	}
	if mock.callCount != 0 {
		t.Errorf("expected 0 LLM calls (nothing to compress), got %d", mock.callCount)
	}
}

func TestSummarizeWithCompression(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Role: "user", Content: "Original request", Tokens: 10, Pinned: true})
	state.AddTurn(Turn{Role: "assistant", Content: "Tool call details here with lots of content", Tokens: 50})
	state.AddTurn(Turn{Role: "system", Content: "More tool output", Tokens: 30})

	mock := &mockLLMClient{response: "Compressed: user requested X, assistant executed tools resulting in Y."}
	c := NewCompressor(mock, cfg)

	result, err := c.Summarize(state, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CompressedCount != 2 {
		t.Errorf("expected 2 compressed, got %d", result.CompressedCount)
	}
	if result.KeptCount != 2 {
		t.Errorf("expected 2 kept (original + summary), got %d", result.KeptCount)
	}
	if result.TokensSaved != 80 {
		t.Errorf("expected 80 tokens saved, got %d", result.TokensSaved)
	}
	if result.Summary != "Compressed: user requested X, assistant executed tools resulting in Y." {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
	if !state.Compressed {
		t.Error("expected state to be marked compressed")
	}
}

func TestSummarizeAggressive(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Role: "user", Content: "Request", Tokens: 5, Pinned: true})
	state.AddTurn(Turn{Role: "assistant", Content: "Long tool output with many details", Tokens: 100})

	mock := &mockLLMClient{response: "Aggressive summary"}
	c := NewCompressor(mock, cfg)

	result, err := c.Summarize(state, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CompressedCount != 1 {
		t.Errorf("expected 1 compressed, got %d", result.CompressedCount)
	}
}

func TestSummarizeLLMError(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	state := NewSessionState(cfg)
	// First turn (original request) — kept by SelectiveRetention
	state.AddTurn(Turn{Role: "user", Content: "Original request", Tokens: 10, Pinned: true})
	// Second turn — not pinned, will be compressed → triggers LLM call
	state.AddTurn(Turn{Role: "assistant", Content: "Some content with lots of details to compress", Tokens: 20})

	mock := &mockLLMClient{response: "", err: fmt.Errorf("API timeout")}
	c := NewCompressor(mock, cfg)

	result, err := c.Summarize(state, false)
	if err == nil {
		t.Fatal("expected error from LLM failure")
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}

func TestCompressOldestN(t *testing.T) {
	turns := []Turn{
		{Pinned: true},  // index 0 - pinned, skip
		{Pinned: false}, // index 1 - not pinned, select
		{Pinned: false}, // index 2 - not pinned, select
		{Pinned: true},  // index 3 - pinned, skip
	}

	indices := CompressOldestN(turns, 2)
	if len(indices) != 2 {
		t.Errorf("expected 2 indices, got %d", len(indices))
	}
	// Should select from the end (oldest first in reverse order)
	if indices[0] != 2 || indices[1] != 1 {
		t.Errorf("expected [2,1], got %v", indices)
	}
}

func TestCompressOldestNMoreThanAvailable(t *testing.T) {
	turns := []Turn{
		{Pinned: true},
		{Pinned: false},
	}

	indices := CompressOldestN(turns, 10) // ask for more than available
	if len(indices) != 1 {
		t.Errorf("expected 1 index (only 1 available), got %d", len(indices))
	}
}

func TestEstimateTokensSaved(t *testing.T) {
	turns := []Turn{
		{Tokens: 10},
		{Tokens: 20},
		{Tokens: 30},
	}
	indices := []int{0, 2}

	saved := EstimateTokensSaved(turns, indices)
	if saved != 40 {
		t.Errorf("expected 40 tokens saved, got %d", saved)
	}
}

func TestEstimateTokensSavedEmpty(t *testing.T) {
	var turns []Turn
	indices := []int{}

	saved := EstimateTokensSaved(turns, indices)
	if saved != 0 {
		t.Errorf("expected 0, got %d", saved)
	}
}

func TestCompressorWithPinnedAndNonPinned(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	state := NewSessionState(cfg)

	// First turn (original request) - pinned by SelectiveRetention
	state.AddTurn(Turn{Role: "user", Content: "Original prompt", Tokens: 10, Pinned: true})
	// Second turn - not pinned, should be compressed
	state.AddTurn(Turn{Role: "assistant", Content: "Detailed tool output with many parameters and results that take up a lot of space in the context window", Tokens: 80})
	// Third turn - not pinned, should be compressed
	state.AddTurn(Turn{Role: "system", Content: "More verbose output", Tokens: 40})
	// Fourth turn - pinned manually
	state.AddTurn(Turn{Role: "assistant", Content: "Final decision", Tokens: 15, Pinned: true})

	mock := &mockLLMClient{response: "User asked X. Assistant ran tools producing Y and Z. Final decision made."}
	c := NewCompressor(mock, cfg)

	result, err := c.Summarize(state, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should keep turns 0 (original), 3 (pinned), plus the summary turn = 3 total
	if result.KeptCount != 3 {
		t.Errorf("expected 3 kept turns, got %d", result.KeptCount)
	}
	if result.CompressedCount != 2 {
		t.Errorf("expected 2 compressed, got %d", result.CompressedCount)
	}

	// Verify the summary turn is pinned
	summaryTurn := state.Turns[len(state.Turns)-1]
	if !summaryTurn.Pinned {
		t.Error("expected summary turn to be pinned")
	}
}

func TestCompressOldestNEmpty(t *testing.T) {
	var turns []Turn
	indices := CompressOldestN(turns, 5)
	if len(indices) != 0 {
		t.Errorf("expected 0 indices for empty input, got %d", len(indices))
	}
}
