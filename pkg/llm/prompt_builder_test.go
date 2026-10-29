package llm

import (
	"os"
	"testing"
	"time"

	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/skills"
)

func TestNewPromptBuilder(t *testing.T) {
	pb := NewPromptBuilder("You are a helpful assistant.", nil, nil)
	if pb == nil {
		t.Fatal("expected non-nil PromptBuilder")
	}
}

func TestBuildContext_Empty(t *testing.T) {
	pb := NewPromptBuilder("system prompt", nil, nil)
	messages, _, _ := pb.BuildContext(nil, nil)

	if len(messages) != 1 {
		t.Errorf("expected 1 message (system), got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Errorf("expected role 'system', got '%s'", messages[0].Role)
	}
	if messages[0].Content != "system prompt" {
		t.Errorf("unexpected content: %q", messages[0].Content)
	}
}

func TestBuildContext_WithSessionHistory(t *testing.T) {
	pb := NewPromptBuilder("system prompt", nil, nil)
	history := []map[string]interface{}{
		{"role": "user", "content": "Hello!"},
		{"role": "assistant", "content": "Hi there! How can I help?"},
	}

	messages, _, _ := pb.BuildContext(history, nil)

	if len(messages) != 3 { // system + 2 history turns
		t.Errorf("expected 3 messages, got %d", len(messages))
	}
	if messages[1].Role != "user" || messages[1].Content != "Hello!" {
		t.Errorf("unexpected message[1]: %+v", messages[1])
	}
	if messages[2].Role != "assistant" || messages[2].Content != "Hi there! How can I help?" {
		t.Errorf("unexpected message[2]: %+v", messages[2])
	}
}

func TestBuildContext_SkipEmptyMessages(t *testing.T) {
	pb := NewPromptBuilder("system prompt", nil, nil)
	history := []map[string]interface{}{
		{"role": "user", "content": ""},           // should be skipped
		{"role": "", "content": "no role"},        // should be skipped
		{"role": "assistant", "content": "valid"}, // should be kept
	}

	messages, _, _ := pb.BuildContext(history, nil)

	if len(messages) != 2 { // system + 1 valid history turn
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
}

func TestBuildContext_WithRawSources(t *testing.T) {
	rs, err := memory.NewRawSources("/tmp/test-raw-sources")
	if err != nil {
		t.Fatalf("failed to create RawSources: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll("/tmp/test-raw-sources") })

	_, _ = rs.Snapshot(memory.Source{
		Name:      "config.yaml",
		ID:        "cfg-001",
		Category:  "config",
		Content:   "key: value\nanother: data",
		CreatedAt: time.Now(),
	})

	pb := NewPromptBuilder("system prompt", rs, nil)
	messages, _, _ := pb.BuildContext(nil, nil)

	// Should have system message + raw sources block
	foundEnvBlock := false
	for _, m := range messages {
		if m.Role == "user" && len(m.Content) > 0 && m.Content[0] == '#' {
			foundEnvBlock = true
			break
		}
	}
	if !foundEnvBlock {
		t.Error("expected environment context block in messages")
	}
}

func TestBuildContext_WithSkills(t *testing.T) {
	skillsList := []skills.SkillEntry{
		{Name: "git", Description: "Git operations"},
		{Name: "docker", Description: "Docker management"},
		{Name: "web-scraper", Description: "Web scraping"},
	}

	pb := NewPromptBuilder("system prompt", nil, skillsList)
	messages, _, _ := pb.BuildContext(nil, []string{"git", "docker"})

	// Should have system + skill context block for git and docker (not web-scraper)
	foundSkillBlock := false
	for _, m := range messages {
		if m.Role == "user" && len(m.Content) > 0 {
			if containsString(m.Content, "### Skill: git") && containsString(m.Content, "### Skill: docker") {
				foundSkillBlock = true
				break
			}
		}
	}
	if !foundSkillBlock {
		t.Error("expected skill context block for active skills")
	}
}

func TestBuildContext_WithBudget(t *testing.T) {
	cfg := budget.NewBudgetConfig(100) // small budget
	sstate := budget.NewSessionState(cfg)
	// Manually set TokenCount to exceed threshold (75% of 100 = 75)
	sstate.TokenCount = 80

	pb := NewPromptBuilder("system prompt", nil, nil).WithBudget(sstate)

	// Add many messages to trigger compression
	var history []map[string]interface{}
	for i := 0; i < 20; i++ {
		history = append(history, map[string]interface{}{
			"role":    "user",
			"content": string(rune('a' + i%26)),
		})
	}

	messages, _, _ := pb.BuildContext(history, nil)

	// Should compress to keepRecent (10) + system message
	if len(messages) > 11 { // 1 system + 10 recent
		t.Errorf("expected <= 11 messages after compression, got %d", len(messages))
	}
}

func TestEstimateTokens(t *testing.T) {
	pb := NewPromptBuilder("test", nil, nil)

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello world!"},
	}

	tokens := pb.estimateTokens(messages)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}

	// Empty messages should yield zero tokens
	tokens = pb.estimateTokens(nil)
	if tokens != 0 {
		t.Errorf("expected 0 tokens for nil messages, got %d", tokens)
	}
}

func TestCompressIfNeeded_NoBudget(t *testing.T) {
	pb := NewPromptBuilder("system", nil, nil)

	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
	}

	result := pb.compressIfNeeded(messages)
	if len(result) != 3 { // No budget = no compression
		t.Errorf("expected 3 messages without budget, got %d", len(result))
	}
}

func TestCompressIfNeeded_ThresholdWarning(t *testing.T) {
	cfg := budget.NewBudgetConfig(100) // small budget to trigger thresholds quickly
	sstate := budget.NewSessionState(cfg)
	// Add turns to exceed warning threshold (75% of 100 = 75 tokens)
	for i := 0; i < 20; i++ {
		sstate.AddTurn(budget.Turn{Role: "user", Content: string(rune('a' + i%26)), Tokens: 5})
	}

	pb := NewPromptBuilder("system", nil, nil).WithBudget(sstate)

	// Build many messages to exceed keepRecent limit (10)
	var messages []Message
	messages = append(messages, Message{Role: "system", Content: "You are helpful."})
	for i := 0; i < 25; i++ {
		messages = append(messages, Message{Role: "user", Content: string(rune('a' + i%26))})
	}

	result := pb.compressIfNeeded(messages)
	if len(result) > 11 { // Should compress to keepRecent(10)+system
		t.Errorf("expected <= 11 messages after compression, got %d", len(result))
	}
}

func TestCompressIfNeeded_ThresholdCritical(t *testing.T) {
	cfg := budget.NewBudgetConfig(50) // very small budget to trigger critical
	sstate := budget.NewSessionState(cfg)
	for i := 0; i < 15; i++ {
		sstate.AddTurn(budget.Turn{Role: "user", Content: string(rune('a' + i%26)), Tokens: 5})
	}

	pb := NewPromptBuilder("system", nil, nil).WithBudget(sstate)

	var messages []Message
	messages = append(messages, Message{Role: "system", Content: "You are helpful."})
	for i := 0; i < 25; i++ {
		messages = append(messages, Message{Role: "user", Content: string(rune('a' + i%26))})
	}

	result := pb.compressIfNeeded(messages)
	if len(result) > 11 {
		t.Errorf("expected <= 11 messages at critical threshold, got %d", len(result))
	}
}

func TestCompressIfNeeded_KeepSystem(t *testing.T) {
	cfg := budget.NewBudgetConfig(100)
	sstate := budget.NewSessionState(cfg)
	for i := 0; i < 20; i++ {
		sstate.AddTurn(budget.Turn{Role: "user", Content: string(rune('a' + i%26)), Tokens: 5})
	}

	pb := NewPromptBuilder("system", nil, nil).WithBudget(sstate)

	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}

	result := pb.compressIfNeeded(messages)
	if len(result) != 2 { // Too few to compress
		t.Errorf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Error("system message should always be kept")
	}
}

func TestCompressIfNeeded_NoThreshold(t *testing.T) {
	cfg := budget.NewBudgetConfig(10000) // large budget, won't trigger
	sstate := budget.NewSessionState(cfg)

	pb := NewPromptBuilder("system", nil, nil).WithBudget(sstate)

	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}

	result := pb.compressIfNeeded(messages)
	if len(result) != 2 { // No compression needed
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

// Helper: check if string contains substring (replaces the one in client.go)
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
