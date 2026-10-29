// Package budget provides token-aware session management for the Norman agent.
// This file implements the compression engine that summarizes older turns
// while preserving critical state (pinned entries, workspace config, plan steps).
package budget

import (
	"context"
	"fmt"
	"strings"
)

// LLMClient is a minimal interface for calling an LLM to generate summaries.
type LLMClient interface {
	// Complete generates a text completion given a prompt.
	Complete(ctx context.Context, prompt string) (string, error)
}

// Compressor handles the summarization of conversation turns into compressed representations.
type Compressor struct {
	client LLMClient
	config *BudgetConfig
}

// NewCompressor creates a compressor with the given LLM client and budget config.
func NewCompressor(client LLMClient, cfg *BudgetConfig) *Compressor {
	return &Compressor{
		client: client,
		config: cfg,
	}
}

// CompressionResult holds the outcome of a compression cycle.
type CompressionResult struct {
	CompressedCount int    // number of turns removed
	KeptCount       int    // number of turns retained
	Summary         string // the generated summary text
	TokensSaved     int    // estimated tokens saved by compression
}

// Summarize performs a compression cycle on the given session state.
// It selects oldest non-pinned turns for summarization and replaces them
// with a single compressed summary turn.
func (c *Compressor) Summarize(state *SessionState, aggressive bool) (*CompressionResult, error) {
	if len(state.Turns) == 0 {
		return nil, ErrNoTurns
	}

	// Determine which turns to keep (not compress)
	keep := SelectiveRetention(state.Turns)

	// Identify turns to compress: oldest non-kept turns
	var compressIndices []int
	for i := range state.Turns {
		if !keep[i] {
			compressIndices = append(compressIndices, i)
		}
	}

	if len(compressIndices) == 0 {
		// Nothing to compress — all turns are pinned or retained
		return &CompressionResult{
			CompressedCount: 0,
			KeptCount:       len(state.Turns),
			Summary:         "All turns preserved (no compression needed).",
			TokensSaved:     0,
		}, nil
	}

	// Build the compression prompt
	prompt := c.buildCompressionPrompt(state, compressIndices, aggressive)

	// Call LLM to generate summary
	ctx := context.Background()
	summary, err := c.client.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM compression failed: %w", err)
	}

	// Calculate tokens saved
	tokensSaved := 0
	for _, idx := range compressIndices {
		tokensSaved += state.Turns[idx].Tokens
	}

	// Replace compressed turns with a single summary turn
	newTurn := Turn{
		Role:      "system",
		Content:   "[COMPRESSED] " + summary,
		Tokens:    len(summary) / 4, // rough estimate: ~4 chars per token
		Pinned:    true,             // compressed summaries are always kept
		CreatedAt: state.Turns[compressIndices[0]].CreatedAt,
	}

	// Build new turns list: keep non-compressed + summary
	var newTurns []Turn
	for i, t := range state.Turns {
		if keep[i] {
			newTurns = append(newTurns, t)
		}
	}
	newTurns = append(newTurns, newTurn)

	state.Turns = newTurns
	state.Compressed = true

	return &CompressionResult{
		CompressedCount: len(compressIndices),
		KeptCount:       len(newTurns),
		Summary:         summary,
		TokensSaved:     tokensSaved,
	}, nil
}

// buildCompressionPrompt constructs the prompt for the LLM compression task.
func (c *Compressor) buildCompressionPrompt(state *SessionState, compressIndices []int, aggressive bool) string {
	var sb strings.Builder

	sb.WriteString("You are a session compression assistant. Your job is to summarize conversation turns into a concise representation while preserving all critical information.\n\n")

	// Include the original content of turns to be compressed
	sb.WriteString("Original turns to compress:\n\n")
	for _, idx := range compressIndices {
		t := state.Turns[idx]
		roleLabel := strings.ToUpper(t.Role)
		if len(t.Content) > 500 {
			fmt.Fprintf(&sb, "[%s] (truncated, %d chars): %s...\n", roleLabel, len(t.Content), t.Content[:497])
		} else {
			fmt.Fprintf(&sb, "[%s] %s\n", roleLabel, t.Content)
		}
	}

	if aggressive {
		sb.WriteString("\n---\n")
		sb.WriteString("AGGRESSIVE MODE: Produce a very concise summary (max 200 words). Focus on outcomes, decisions, and key facts. Omit tool call details.\n\n")
	} else {
		sb.WriteString("\n---\n")
		sb.WriteString("MODE: Produce a detailed but compressed summary (~500 words max). Preserve tool results and important context.\n\n")
	}

	sb.WriteString("Rules:\n")
	sb.WriteString("- Never omit pinned entries or the original user request\n")
	sb.WriteString("- Preserve key decisions, outcomes, and task state\n")
	sb.WriteString("- Use bullet points for clarity\n")
	sb.WriteString("- Do NOT add new information not present in the turns\n")
	sb.WriteString("\nProvide only the compressed summary text. No preamble.\n")

	return sb.String()
}

// CompressOldestN selects the N oldest non-pinned turns for compression.
func CompressOldestN(turns []Turn, n int) []int {
	var indices []int
	kept := make(map[int]bool)
	for i := len(turns) - 1; i >= 0 && len(indices) < n; i-- {
		if !turns[i].Pinned {
			indices = append(indices, i)
			kept[i] = true
		}
	}
	return indices
}

// EstimateTokensSaved calculates how many tokens would be saved by compressing given indices.
func EstimateTokensSaved(turns []Turn, indices []int) int {
	saved := 0
	for _, idx := range indices {
		saved += turns[idx].Tokens
	}
	return saved
}
