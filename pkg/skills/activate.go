package skills

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/norman/norman/pkg/env"
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
// If session is non-nil, dependencies are installed inside that OpenShell sandbox.
// Returns the injected tool definitions as a string (for prompt injection).
func (a *Activator) Activate(name string, session *env.Session) (string, error) {
	entry, ok := a.registry[name]
	if !ok {
		return "", fmt.Errorf("skill '%s' not found in registry", name)
	}

	// Install dependencies before activation (non-fatal)
	if len(entry.Dependencies) > 0 {
		if err := InstallDependencies(entry, session); err != nil {
			// Log warning but don't block activation
			log.Printf("[skills] dependency install for '%s': %v", name, err)
		}
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

// DeployToSandbox ensures a skill's files are present inside the given OpenShell sandbox.
// If the skill is already deployed in this sandbox, it is a no-op (idempotent).
// For git-based skills: clones into /workspace/<ws-id>/skills/<name>/ and runs install.sh.
// For binary skills (BinaryPath set): copies the host binary to ~/.local/bin/<name>.
// Returns the deployed SkillEntry or an error.
func (a *Activator) DeployToSandbox(name string, session *env.Session) (*SkillEntry, error) {
	if session == nil {
		return nil, fmt.Errorf("DeployToSandbox requires a non-nil session")
	}

	sandboxName := session.SessionID()
	ctx := context.Background()

	// Get entry from registry to find source_url/version/binary_path
	entry, ok := a.registry[name]
	if !ok {
		return nil, fmt.Errorf("skill '%s' not found in registry", name)
	}

	// Binary skills: copy pre-built binary to ~/.local/bin/<name>
	if entry.BinaryPath != "" {
		return a.deployBinary(name, session, &entry, ctx)
	}

	// Git-based skill: clone into /workspace/<ws-id>/skills/<name>/
	skillDir := filepath.Join("/workspace", sandboxName, "skills", name)

	// Check if skill is already deployed in this sandbox
	checkCmd := fmt.Sprintf("test -d %s && echo 'exists'", skillDir)
	output, err := session.Exec(ctx, []string{"bash", "-c", checkCmd})
	if err == nil && strings.Contains(strings.TrimSpace(output), "exists") {
		log.Printf("[skills] '%s' already deployed in sandbox '%s', skipping clone", name, sandboxName)
		entry, err := readSkillMetadata(skillDir)
		if err != nil {
			return nil, fmt.Errorf("parse SKILL.md in sandbox '%s': %w", sandboxName, err)
		}
		return &entry, nil
	}

	// Clone into sandbox via session.Exec
	cloneCmd := fmt.Sprintf("git clone --depth 1 %s %s && cd %s && git checkout %s 2>/dev/null || true",
		entry.SourceURL, skillDir, skillDir, entry.Version)
	output, err = session.Exec(ctx, []string{"bash", "-c", cloneCmd})
	if err != nil {
		return nil, fmt.Errorf("git clone into sandbox '%s': %w (output: %s)", sandboxName, err, output)
	}

	// Run platform-specific install if SKILL.md references it (e.g., graphify install --platform agents)
	installCmd := fmt.Sprintf("cd %s && if [ -f install.sh ]; then bash install.sh; fi", skillDir)
	output, err = session.Exec(ctx, []string{"bash", "-c", installCmd})
	if err != nil {
		log.Printf("[skills] platform install step for '%s' in sandbox '%s': %v", name, sandboxName, output)
	}

	// Parse and return the deployed entry
	entry, err = readSkillMetadata(skillDir)
	if err != nil {
		return nil, fmt.Errorf("parse SKILL.md in sandbox '%s': %w", sandboxName, err)
	}
	return &entry, nil
}

// deployBinary copies a pre-built host binary into the sandbox at ~/.local/bin/<name>.
func (a *Activator) deployBinary(name string, session *env.Session, entry *SkillEntry, ctx context.Context) (*SkillEntry, error) {
	sandboxName := session.SessionID()
	targetPath := filepath.Join("~", ".local", "bin", name)

	// Check if already deployed (idempotent)
	checkCmd := fmt.Sprintf("test -f %s && echo 'exists'", targetPath)
	output, err := session.Exec(ctx, []string{"bash", "-c", checkCmd})
	if err == nil && strings.Contains(strings.TrimSpace(output), "exists") {
		log.Printf("[skills] '%s' already deployed in sandbox '%s', skipping copy", name, sandboxName)
		return entry, nil
	}

	// Copy binary from host path into sandbox home directory
	copyCmd := fmt.Sprintf("cp %s %s && chmod +x %s", entry.BinaryPath, targetPath, targetPath)
	output, err = session.Exec(ctx, []string{"bash", "-c", copyCmd})
	if err != nil {
		return nil, fmt.Errorf("copy binary '%s' into sandbox '%s': %w (output: %s)", name, sandboxName, err, output)
	}

	// Ensure ~/.local/bin is in PATH by appending to .bashrc
	pathCmd := fmt.Sprintf(`grep -q '$HOME/.local/bin' "$HOME/.bashrc" 2>/dev/null || echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.bashrc"`)
	output, err = session.Exec(ctx, []string{"bash", "-c", pathCmd})
	if err != nil {
		log.Printf("[skills] PATH injection for '%s' in sandbox '%s': %v", name, sandboxName, output)
	}

	log.Printf("[skills] deployed binary skill '%s' → %s in sandbox '%s'", name, targetPath, sandboxName)
	return entry, nil
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

// WorkspaceContext represents the workspace identity used for skill activation matching.
type WorkspaceContext struct {
	Name    string
	Purpose string
	Scope   []string
	Tags    []string
}

// MatchSkillWithWorkspace checks if a skill should be activated based on both
// task keywords AND workspace context (tags/purpose). This enables proactive
// skill activation grounded in the workspace's domain identity.
func MatchSkillWithWorkspace(entry SkillEntry, taskKeywords []string, ws *WorkspaceContext) bool {
	if entry.Activation == "auto" {
		return true // always active for auto skills
	}

	// For on-demand/manual: check if any keyword matches the skill name or tools
	lowerName := strings.ToLower(entry.Name)
	lowerDesc := strings.ToLower(entry.Description)

	matched := false
	for _, kw := range taskKeywords {
		if strings.Contains(lowerName, strings.ToLower(kw)) ||
			strings.Contains(lowerDesc, strings.ToLower(kw)) {
			matched = true
			break
		}
	}

	// If workspace context is provided, boost match score by checking workspace tags/purpose/scope
	if ws != nil && !matched {
		lowerPurpose := strings.ToLower(ws.Purpose)
		lowerName := strings.ToLower(entry.Name)
		lowerDesc := strings.ToLower(entry.Description)

		// Check if any tag matches the skill name, description, or purpose
		for _, tag := range ws.Tags {
			if strings.Contains(lowerName, strings.ToLower(tag)) ||
				strings.Contains(lowerDesc, strings.ToLower(tag)) ||
				strings.Contains(lowerPurpose, strings.ToLower(tag)) {
				return true
			}
		}

		// Check if any scope item matches the skill name or description
		for _, scope := range ws.Scope {
			if strings.Contains(lowerName, strings.ToLower(scope)) ||
				strings.Contains(lowerDesc, strings.ToLower(scope)) {
				return true
			}
		}

		// Direct purpose keyword match (e.g., "grpc" in purpose matches "grpc-client")
		for _, kw := range strings.Fields(lowerPurpose) {
			if len(kw) >= 3 && (strings.Contains(lowerName, kw) || strings.Contains(lowerDesc, kw)) {
				return true
			}
		}
	}

	return matched
}

// Registry returns the full skill registry map.
func (a *Activator) Registry() map[string]SkillEntry {
	return a.registry
}

// ToolRegistry is the interface that the agent loop uses to query available tools.
type ToolRegistry interface {
	// GetTools returns tool definitions for a specific skill by name.
	GetTools(name string) ([]string, error)
	// ListActive returns currently active skill names.
	ListActive() []string
}


