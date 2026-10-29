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

// CheckThresholds returns which thresholds have been breached.
// Returns a bitmask of ThresholdWarn and/or ThresholdCritical.
func (s *SessionState) CheckThresholds() ThresholdFlags {
	flags := ThresholdFlags(0)
	usedFraction := float64(s.TokenCount) / float64(s.Budget.MaxTokens)

	if usedFraction >= s.Budget.CriticalThreshold {
		flags |= ThresholdCritical
	}
	if usedFraction >= s.Budget.WarnThreshold {
		flags |= ThresholdWarn
	}
	return flags
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

// ThresholdFlags represents which budget thresholds have been triggered.
type ThresholdFlags int

const (
	ThresholdNone     ThresholdFlags = 0
	ThresholdWarn     ThresholdFlags = 1 << 0 // 75% threshold
	ThresholdCritical ThresholdFlags = 1 << 1 // 90% threshold
)

// Is returns true if the flags contain the given flag.
func (f ThresholdFlags) Is(flag ThresholdFlags) bool {
	return f&flag != 0
}

// String returns a human-readable representation of the flags.
func (f ThresholdFlags) String() string {
	if f == ThresholdNone {
		return "none"
	}
	var parts []string
	if f.Is(ThresholdWarn) {
		parts = append(parts, "warn")
	}
	if f.Is(ThresholdCritical) {
		parts = append(parts, "critical")
	}
	return strings.Join(parts, ", ")
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
