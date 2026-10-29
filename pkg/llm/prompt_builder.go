// Package llm provides a prompt builder that assembles conversation context
// from session history, raw environment sources, and active skill definitions
// into a structured message array suitable for LLM API calls.
package llm

import (
	"fmt"
	"strings"
	"time"

	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/skills"
)

// PromptBuilder assembles a system prompt and conversation messages from
// multiple context sources: session history, raw environment data, and skills.
type PromptBuilder struct {
	systemPrompt string
	rawSources   *memory.RawSources
	skillEntries []skills.SkillEntry
	budget       *budget.SessionState
}

// NewPromptBuilder creates a builder configured with context sources.
func NewPromptBuilder(systemPrompt string, rawSources *memory.RawSources, skills []skills.SkillEntry) *PromptBuilder {
	return &PromptBuilder{
		systemPrompt: systemPrompt,
		rawSources:   rawSources,
		skillEntries: skills,
	}
}

// WithBudget configures the builder with a budget tracker for token-aware compression.
func (b *PromptBuilder) WithBudget(cfg *budget.SessionState) *PromptBuilder {
	b.budget = cfg
	return b
}

// BuildContext assembles the full context window for an LLM API call.
// It returns: system message content, conversation messages array, and estimated token count.
func (b *PromptBuilder) BuildContext(sessionHistory []map[string]interface{}, activeSkills []string) ([]Message, string, int) {
	var messages []Message

	// 1. System prompt
	messages = append(messages, Message{
		Role:    "system",
		Content: b.systemPrompt,
	})

	// 2. Raw environment context (injected once at the start of conversation)
	if b.rawSources != nil {
		sources, err := b.rawSources.ListAll()
		if err == nil && len(sources) > 0 {
			var ctxParts []string
			ctxParts = append(ctxParts, "## Environment Context")
			ctxParts = append(ctxParts, "")
			for _, s := range sources {
				ctxParts = append(ctxParts, fmt.Sprintf("### %s (%s)", s.Name, s.ID))
				ctxParts = append(ctxParts, fmt.Sprintf("_Created: %s | Category: %s_", s.CreatedAt.Format(time.RFC3339), s.Category))
				ctxParts = append(ctxParts, "")
				ctxParts = append(ctxParts, "```")
				ctxParts = append(ctxParts, s.Content)
				ctxParts = append(ctxParts, "```")
				ctxParts = append(ctxParts, "")
			}
			messages = append(messages, Message{
				Role:    "user",
				Content: strings.Join(ctxParts, "\n"),
			})
		}
	}

	// 3. Active skills context
	if len(activeSkills) > 0 {
		var skillContexts []string
		for _, entry := range b.skillEntries {
			if contains(activeSkills, entry.Name) {
				skillContexts = append(skillContexts, fmt.Sprintf("### Skill: %s\n%s", entry.Name, entry.Description))
			}
		}
		if len(skillContexts) > 0 {
			messages = append(messages, Message{
				Role:    "user",
				Content: strings.Join(skillContexts, "\n\n"),
			})
		}
	}

	// 4. Session history (conversation turns)
	if len(sessionHistory) > 0 {
		for _, turn := range sessionHistory {
			role, _ := turn["role"].(string)
			content, _ := turn["content"].(string)
			if role == "" || content == "" {
				continue
			}
			messages = append(messages, Message{
				Role:    role,
				Content: content,
			})
		}
	}

	// 5. Budget-aware compression: if budget is near threshold, truncate older turns
	if b.budget != nil {
		messages = b.compressIfNeeded(messages)
	}

	return messages, "", b.estimateTokens(messages)
}

// compressIfNeeded reduces the message array if token budget thresholds are approaching.
func (b *PromptBuilder) compressIfNeeded(messages []Message) []Message {
	if b.budget == nil {
		return messages
	}

	flags := b.budget.CheckThresholds()
	if flags == budget.ThresholdNone {
		return messages
	}

	// Keep system message + last N user/assistant turns, drop older ones
	const keepRecent = 10 // Keep last 10 conversation turns
	var recent []Message

	// Always keep system message
	recent = append(recent, messages[0])

	// Keep the most recent turns (skip older ones)
	if len(messages) > keepRecent+1 { // +1 for system message
		recent = append(recent, messages[len(messages)-keepRecent:]...)
	} else {
		recent = append(recent, messages[1:]...)
	}

	return recent
}

// estimateTokens provides a rough token count estimate for the assembled messages.
func (b *PromptBuilder) estimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += budget.CountTokens(m.Content)
	}
	return total
}

// contains checks if a string slice contains a value.
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
