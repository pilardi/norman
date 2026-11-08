// Package budget provides token-aware session management for the Norman agent.
// It tracks cumulative token usage across conversation turns and triggers
// compression cycles when configured thresholds are reached.
package budget

import (
	"fmt"
	"strings"
	"time"
)

// EstimationMode controls how token counts are determined.
type EstimationMode string

const (
	// EstimationAPI uses the LLM API's usage.token_count field (most accurate).
	EstimationAPI EstimationMode = "api"
	// EstimationTiktoken uses github.com/pkoukk/tiktoken-go for estimation.
	EstimationTiktoken EstimationMode = "tiktoken"
)

// BudgetConfig holds the configuration for token budget tracking.
type BudgetConfig struct {
	MaxTokens         int            // total token budget (e.g., 128000 for GPT-4)
	WarnThreshold     float64        // trigger at this fraction of max (e.g., 0.75)
	CriticalThreshold float64        // trigger at this fraction of max (e.g., 0.90)
	Estimation        EstimationMode // how to count tokens
}

// NewBudgetConfig creates a config with reasonable defaults.
func NewBudgetConfig(maxTokens int) *BudgetConfig {
	return &BudgetConfig{
		MaxTokens:         maxTokens,
		WarnThreshold:     0.75,
		CriticalThreshold: 0.90,
		Estimation:        EstimationAPI,
	}
}

// Turn represents a single conversation turn in the session history.
type Turn struct {
	Role      string // "user", "assistant", "system"
	Content   string
	Tokens    int  // estimated or actual token count for this turn
	Pinned    bool // if true, never compressed out during summarization
	CreatedAt time.Time
}

// SessionState tracks the current state of a session including budget usage.
type SessionState struct {
	Turns       []Turn
	Budget      *BudgetConfig
	Compressed  bool      // flag: has a compression cycle run?
	LastCheck   time.Time // when budget was last checked
	TokenCount  int       // cumulative tokens used so far
	PlanID      string    // optional plan identifier for this session
	PlanContent string    // optional plan content (preserved across compression)
}

// NewSessionState creates a new session state with the given budget config.
func NewSessionState(cfg *BudgetConfig) *SessionState {
	return &SessionState{
		Budget:     cfg,
		LastCheck:  time.Now(),
		TokenCount: 0,
	}
}

// AddTurn appends a turn and updates the cumulative token count.
func (s *SessionState) AddTurn(turn Turn) {
	s.Turns = append(s.Turns, turn)
	s.TokenCount += turn.Tokens
	s.LastCheck = time.Now()
}

// CurrentState returns the current budget state as a single enum value.
// The state is monotonically increasing: none → warn → critical (never decreases).
func (s *SessionState) CurrentState() BudgetState {
	usedFraction := float64(s.TokenCount) / float64(s.Budget.MaxTokens)

	if usedFraction >= s.Budget.CriticalThreshold {
		return BudgetCritical
	}
	if usedFraction >= s.Budget.WarnThreshold {
		return BudgetWarn
	}
	return BudgetNone
}

// UsedFraction returns the fraction of budget consumed (0.0 to 1.0+).
func (s *SessionState) UsedFraction() float64 {
	if s.Budget.MaxTokens == 0 {
		return 0
	}
	return float64(s.TokenCount) / float64(s.Budget.MaxTokens)
}

// Remaining returns tokens still available in the budget.
func (s *SessionState) Remaining() int {
	remaining := s.Budget.MaxTokens - s.TokenCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

// BudgetState represents the current budget threshold state.
type BudgetState string

const (
	BudgetNone     BudgetState = "none"     // below warn threshold (<75%)
	BudgetWarn     BudgetState = "warn"     // at or above warn threshold (>=75%)
	BudgetCritical BudgetState = "critical" // at or above critical threshold (>=90%)
)

// String returns the human-readable representation of the budget state.
func (s BudgetState) String() string {
	return string(s)
}

// CountTokens estimates the number of tokens in a string using simple heuristics.
// This is a fallback when API-provided counts are unavailable.
func CountTokens(text string) int {
	if text == "" {
		return 0
	}
	// Simple heuristic: ~4 chars per token for English text
	// This is a rough estimate; tiktoken would be more accurate.
	wordCount := len(strings.Fields(text))
	charCount := len(strings.Split(text, ""))
	// Heuristic: average token length is ~4 characters, words are ~5 chars
	return wordCount + charCount/10
}

// CountTokensFromTurns returns the total tokens across all turns.
func CountTokensFromTurns(turns []Turn) int {
	total := 0
	for _, t := range turns {
		total += t.Tokens
	}
	return total
}

// SelectiveRetention identifies which turns should always be preserved during compression.
func SelectiveRetention(turns []Turn) map[int]bool {
	keep := make(map[int]bool)
	if len(turns) == 0 {
		return keep
	}
	// Always keep the first turn (original request)
	keep[0] = true
	// Keep all pinned turns
	for i, t := range turns {
		if t.Pinned {
			keep[i] = true
		}
	}
	return keep
}

// Error types for budget operations.
var (
	ErrInvalidConfig = fmt.Errorf("invalid budget config")
	ErrNoTurns       = fmt.Errorf("no turns to process")
)
