package budget

import (
	"testing"
)

func TestNewBudgetConfig(t *testing.T) {
	cfg := NewBudgetConfig(128000)
	if cfg.MaxTokens != 128000 {
		t.Errorf("expected MaxTokens=128000, got %d", cfg.MaxTokens)
	}
	if cfg.WarnThreshold != 0.75 {
		t.Errorf("expected WarnThreshold=0.75, got %f", cfg.WarnThreshold)
	}
	if cfg.CriticalThreshold != 0.90 {
		t.Errorf("expected CriticalThreshold=0.90, got %f", cfg.CriticalThreshold)
	}
	if cfg.Estimation != EstimationAPI {
		t.Errorf("expected Estimation=%s, got %s", EstimationAPI, cfg.Estimation)
	}
}

func TestNewSessionState(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	if state.Budget != cfg {
		t.Error("expected Budget to reference config")
	}
	if state.TokenCount != 0 {
		t.Errorf("expected TokenCount=0, got %d", state.TokenCount)
	}
	if state.Compressed {
		t.Error("expected Compressed=false initially")
	}
}

func TestAddTurn(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)

	state.AddTurn(Turn{Role: "user", Content: "Hello", Tokens: 5})
	state.AddTurn(Turn{Role: "assistant", Content: "Hi there!", Tokens: 4})

	if len(state.Turns) != 2 {
		t.Errorf("expected 2 turns, got %d", len(state.Turns))
	}
	if state.TokenCount != 9 {
		t.Errorf("expected TokenCount=9, got %d", state.TokenCount)
	}
	if state.LastCheck.IsZero() {
		t.Error("expected LastCheck to be set")
	}
}

func TestCheckThresholdsNone(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 100}) // 10% used

	flags := state.CheckThresholds()
	if flags.Is(ThresholdWarn) {
		t.Error("expected no warn threshold at 10%")
	}
	if flags.Is(ThresholdCritical) {
		t.Error("expected no critical threshold at 10%")
	}
}

func TestCheckThresholdsWarn(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 750}) // 75% used

	flags := state.CheckThresholds()
	if !flags.Is(ThresholdWarn) {
		t.Error("expected warn threshold at 75%")
	}
	if flags.Is(ThresholdCritical) {
		t.Error("expected no critical threshold at exactly 75%")
	}
}

func TestCheckThresholdsCritical(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 900}) // 90% used

	flags := state.CheckThresholds()
	if !flags.Is(ThresholdWarn) {
		t.Error("expected warn threshold at 90%")
	}
	if !flags.Is(ThresholdCritical) {
		t.Error("expected critical threshold at 90%")
	}
}

func TestCheckThresholdsBoth(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 950}) // 95% used

	flags := state.CheckThresholds()
	if !flags.Is(ThresholdWarn) {
		t.Error("expected warn threshold")
	}
	if !flags.Is(ThresholdCritical) {
		t.Error("expected critical threshold")
	}
}

func TestUsedFraction(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 250}) // 25%

	frac := state.UsedFraction()
	if frac != 0.25 {
		t.Errorf("expected 0.25, got %f", frac)
	}
}

func TestUsedFractionZeroBudget(t *testing.T) {
	cfg := NewBudgetConfig(0) // edge case: zero budget
	state := NewSessionState(cfg)
	frac := state.UsedFraction()
	if frac != 0 {
		t.Errorf("expected 0 with zero budget, got %f", frac)
	}
}

func TestRemaining(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 300})

	if state.Remaining() != 700 {
		t.Errorf("expected 700 remaining, got %d", state.Remaining())
	}
}

func TestRemainingOverflow(t *testing.T) {
	cfg := NewBudgetConfig(1000)
	state := NewSessionState(cfg)
	state.AddTurn(Turn{Tokens: 1500}) // exceeds budget

	if state.Remaining() != 0 {
		t.Errorf("expected 0 remaining (clamped), got %d", state.Remaining())
	}
}

func TestThresholdFlagsString(t *testing.T) {
	if ThresholdNone.String() != "none" {
		t.Errorf("expected 'none', got '%s'", ThresholdNone.String())
	}
	if ThresholdWarn.String() != "warn" {
		t.Errorf("expected 'warn', got '%s'", ThresholdWarn.String())
	}
	if ThresholdCritical.String() != "critical" {
		t.Errorf("expected 'critical', got '%s'", ThresholdCritical.String())
	}
	if (ThresholdWarn | ThresholdCritical).String() != "warn, critical" {
		t.Errorf("expected 'warn, critical', got '%s'", (ThresholdWarn|ThresholdCritical).String())
	}
}

func TestCountTokensEmpty(t *testing.T) {
	n := CountTokens("")
	if n != 0 {
		t.Errorf("expected 0 for empty string, got %d", n)
	}
}

func TestCountTokensSimple(t *testing.T) {
	// "Hello world" = 2 words + 11 chars => 2 + 1 = 3 (rough estimate)
	n := CountTokens("Hello world")
	if n < 1 {
		t.Errorf("expected >= 1 token, got %d", n)
	}
}

func TestCountTokensFromTurns(t *testing.T) {
	turns := []Turn{
		{Tokens: 10},
		{Tokens: 20},
		{Tokens: 30},
	}
	if CountTokensFromTurns(turns) != 60 {
		t.Errorf("expected 60, got %d", CountTokensFromTurns(turns))
	}
}

func TestCountTokensFromTurnsEmpty(t *testing.T) {
	if CountTokensFromTurns(nil) != 0 {
		t.Errorf("expected 0 for nil, got %d", CountTokensFromTurns(nil))
	}
}

func TestSelectiveRetention(t *testing.T) {
	turns := []Turn{
		{Pinned: false}, // index 0 - should be kept (first turn)
		{Pinned: true},  // index 1 - pinned, keep
		{Pinned: false}, // index 2 - not pinned, skip
		{Pinned: true},  // index 3 - pinned, keep
	}

	keep := SelectiveRetention(turns)
	if len(keep) != 3 {
		t.Errorf("expected 3 kept turns (0+1+3), got %d", len(keep))
	}
	if !keep[0] || !keep[1] || keep[2] || !keep[3] {
		t.Errorf("expected indices 0,1,3 kept; got map=%v", keep)
	}
}

func TestSelectiveRetentionEmpty(t *testing.T) {
	keep := SelectiveRetention(nil)
	if len(keep) != 0 {
		t.Errorf("expected empty map for nil turns, got %d entries", len(keep))
	}
}
