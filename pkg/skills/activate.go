package skills

import (
	"fmt"
	"strings"
)

// Activator manages progressive activation of skill tools into agent context.
type Activator struct {
	registry map[string]SkillEntry // name → entry, loaded at startup
	active   []string              // currently active skill names (reclaim budget by deactivating)
	maxActive int                   // max concurrent active skills
}

// NewActivator creates a new activator with the given registry and max active limit.
func NewActivator(registry map[string]SkillEntry, maxActive int) *Activator {
	if maxActive <= 0 {
		maxActive = 3 // default: allow up to 3 concurrent skills
	}
	return &Activator{
		registry:  registry,
		active:    make([]string, 0),
		maxActive: maxActive,
	}
}

// Activate activates a skill's tools into the current context.
// Returns the injected tool definitions as a string (for prompt injection).
func (a *Activator) Activate(name string) (string, error) {
	entry, ok := a.registry[name]
	if !ok {
		return "", fmt.Errorf("skill '%s' not found in registry", name)
	}

	// Check if already active (idempotent)
	for _, active := range a.active {
		if active == name {
			return "", nil // already active, nothing to inject
		}
	}

	// If at capacity, deactivate the least-recently-used skill
	if len(a.active) >= a.maxActive && len(a.active) > 0 {
		a.Deactivate(a.active[0])
	}

	a.active = append(a.active, name)
	return formatToolInjection(entry), nil
}

// Deactivate removes a skill from active context.
func (a *Activator) Deactivate(name string) error {
	for i, active := range a.active {
		if active == name {
			a.active = append(a.active[:i], a.active[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("skill '%s' is not currently active", name)
}

// DeactivateAll removes all active skills, reclaiming token budget.
func (a *Activator) DeactivateAll() {
	a.active = a.active[:0]
}

// Active returns the list of currently active skill names.
func (a *Activator) Active() []string {
	names := make([]string, len(a.active))
	copy(names, a.active)
	return names
}

// formatToolInjection formats a skill's tools into a prompt-injection string.
func formatToolInjection(entry SkillEntry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## Active Skill: %s\n", entry.Name))
	sb.WriteString(fmt.Sprintf("%s\n", entry.Description))

	if len(entry.Tools) > 0 {
		sb.WriteString("\n**Available Tools:**\n")
		for _, tool := range entry.Tools {
			sb.WriteString(fmt.Sprintf("- `%s`\n", tool))
		}
	}

	return sb.String()
}

// MatchSkill checks if a skill should be auto-activated based on task keywords.
func MatchSkill(entry SkillEntry, taskKeywords []string) bool {
	if entry.Activation == "auto" {
		return true // always active for auto skills
	}

	// For on-demand/manual: check if any keyword matches the skill name or tools
	lowerName := strings.ToLower(entry.Name)
	lowerDesc := strings.ToLower(entry.Description)

	for _, kw := range taskKeywords {
		if strings.Contains(lowerName, strings.ToLower(kw)) ||
			strings.Contains(lowerDesc, strings.ToLower(kw)) {
			return true
		}
	}

	return false
}

// ToolRegistry is the interface that the agent loop uses to query available tools.
type ToolRegistry interface {
	// GetTools returns tool definitions for a specific skill by name.
	GetTools(name string) ([]string, error)
	// ListActive returns currently active skill names.
	ListActive() []string
}


