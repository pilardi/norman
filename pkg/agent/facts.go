// Package agent provides immutable preloaded facts about Norman's identity,
// purpose, origin, and capabilities. These facts are baked into the binary
// at build time via Go embed — they cannot be overridden by workspace config
// or user input after installation.
//
// The facts are stored as a YAML file (facts.yaml) and parsed into a typed
// struct for injection into the LLM system prompt.

package agent

import (
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed facts.yaml
var factsYAML []byte

// Facts represents the immutable preloaded identity and context of Norman.
type Facts struct {
	Identity     Identity `yaml:"identity"`
	Purpose      string   `yaml:"purpose"`
	Origin       string   `yaml:"origin"`
	Capabilities string   `yaml:"capabilities"`
	Constraints  string   `yaml:"constraints"`
}

// Identity holds the agent's core identity fields.
type Identity struct {
	Name     string `yaml:"name"`
	Role     string `yaml:"role"`
	Version  string `yaml:"version"`
	Pronouns string `yaml:"pronouns"`
}

// ParseFacts reads the embedded YAML and returns a Facts struct.
// This is called once at startup; errors are fatal (facts must be valid).
func ParseFacts() *Facts {
	var f Facts
	if err := yaml.Unmarshal(factsYAML, &f); err != nil {
		panic(fmt.Sprintf("failed to parse embedded facts.yaml: %v", err))
	}
	return &f
}

// AsSystemPrompt converts the facts into a markdown block suitable for
// injection into the LLM's system prompt. Returns empty string if facts
// are nil (for testing or shell-only mode).
func (f *Facts) AsSystemPrompt() string {
	if f == nil {
		return ""
	}

	var parts []string
	parts = append(parts, "## Agent Identity & Origin")
	parts = append(parts, "")
	parts = append(parts, fmt.Sprintf("**You are %s**, a chat-first AI agent.", f.Identity.Name))
	parts = append(parts, fmt.Sprintf("Version: %s", f.Identity.Version))
	if f.Identity.Pronouns != "" {
		parts = append(parts, fmt.Sprintf("Pronouns: %s", f.Identity.Pronouns))
	}

	if f.Purpose != "" {
		parts = append(parts, "")
		parts = append(parts, "**Purpose:**")
		for _, line := range strings.Split(f.Purpose, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				parts = append(parts, "  "+line)
			}
		}
	}

	if f.Origin != "" {
		parts = append(parts, "")
		parts = append(parts, "**Origin:**")
		for _, line := range strings.Split(f.Origin, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				parts = append(parts, "  "+line)
			}
		}
	}

	if f.Capabilities != "" {
		parts = append(parts, "")
		parts = append(parts, "**Capabilities:**")
		for _, line := range strings.Split(f.Capabilities, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				parts = append(parts, "  "+line)
			}
		}
	}

	if f.Constraints != "" {
		parts = append(parts, "")
		parts = append(parts, "**Constraints (MVP):**")
		for _, line := range strings.Split(f.Constraints, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				parts = append(parts, "  "+line)
			}
		}
	}

	return strings.Join(parts, "\n")
}
