package agent

import (
	"strings"
	"testing"
)

func TestParseFacts(t *testing.T) {
	f := ParseFacts()
	if f == nil {
		t.Fatal("ParseFacts returned nil")
	}
	if f.Identity.Name != "Norman" {
		t.Errorf("expected name 'Norman', got '%s'", f.Identity.Name)
	}
	if f.Identity.Role != "personal AI assistant agent" {
		t.Errorf("expected role 'personal AI assistant agent', got '%s'", f.Identity.Role)
	}
	if f.Identity.Version != "1.0" {
		t.Errorf("expected version '1.0', got '%s'", f.Identity.Version)
	}
	if !strings.Contains(f.Purpose, "Norman is your personal AI assistant agent") {
		t.Errorf("purpose should mention 'Norman is your personal AI assistant agent', got: %s", f.Purpose)
	}
	if !strings.Contains(f.Origin, "pilardi") {
		t.Errorf("origin should mention 'pilardi', got: %s", f.Origin)
	}
}

func TestFactsAsSystemPrompt(t *testing.T) {
	f := ParseFacts()
	prompt := f.AsSystemPrompt()

	if prompt == "" {
		t.Fatal("AsSystemPrompt returned empty string")
	}
	if !strings.Contains(prompt, "## Agent Identity & Origin") {
		t.Error("prompt should contain section header")
	}
	if !strings.Contains(prompt, "You are Norman") {
		t.Error("prompt should mention 'You are Norman'")
	}
	if !strings.Contains(prompt, "**Purpose:**") {
		t.Error("prompt should contain Purpose section")
	}
	if !strings.Contains(prompt, "**Origin:**") {
		t.Error("prompt should contain Origin section")
	}
	if !strings.Contains(prompt, "**Capabilities:**") {
		t.Error("prompt should contain Capabilities section")
	}
	if !strings.Contains(prompt, "**Constraints (MVP):**") {
		t.Error("prompt should contain Constraints section")
	}
}

func TestFactsNilSystemPrompt(t *testing.T) {
	var f *Facts
	prompt := f.AsSystemPrompt()
	if prompt != "" {
		t.Errorf("nil Facts should return empty string, got: %q", prompt)
	}
}
