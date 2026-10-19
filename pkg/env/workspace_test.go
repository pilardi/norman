package env

import (
	"testing"
)

func TestWorkspaceDefinition_ToMap(t *testing.T) {
	def := &WorkspaceDefinition{
		Name:    "test-project",
		Purpose: "API migration for legacy service",
		Scope:   []string{"service-a", "service-b"},
		Tags:    []string{"go", "api", "migration"},
	}

	m := def.ToMap()

	if m["name"] != "test-project" {
		t.Errorf("expected name 'test-project', got '%v'", m["name"])
	}
	if m["purpose"] != "API migration for legacy service" {
		t.Errorf("unexpected purpose: %v", m["purpose"])
	}
	scope, ok := m["scope"].([]interface{})
	if !ok || len(scope) != 2 {
		t.Errorf("expected scope length 2, got %d", len(scope))
	}
	tags, ok := m["tags"].([]interface{})
	if !ok || len(tags) != 3 {
		t.Errorf("expected tags length 3, got %d", len(tags))
	}
}

func TestWorkspaceDefinition_FromMap(t *testing.T) {
	m := map[string]interface{}{
		"name":    "my-workspace",
		"purpose": "Build a Go CLI tool",
		"scope":   []interface{}{"cli", "config"},
		"tags":    []interface{}{"go", "cli-tool"},
	}

	def := WorkspaceDefinitionFromMap(m)

	if def.Name != "my-workspace" {
		t.Errorf("expected name 'my-workspace', got '%s'", def.Name)
	}
	if def.Purpose != "Build a Go CLI tool" {
		t.Errorf("unexpected purpose: %s", def.Purpose)
	}
	if len(def.Scope) != 2 || def.Scope[0] != "cli" {
		t.Errorf("unexpected scope: %v", def.Scope)
	}
	if len(def.Tags) != 2 || def.Tags[0] != "go" {
		t.Errorf("unexpected tags: %v", def.Tags)
	}
}

func TestWorkspaceDefinition_FromMap_Empty(t *testing.T) {
	m := map[string]interface{}{}
	def := WorkspaceDefinitionFromMap(m)

	if def.Name != "" || def.Purpose != "" || len(def.Scope) != 0 || len(def.Tags) != 0 {
		t.Errorf("expected empty definition, got %+v", def)
	}
}

func TestWorkspaceDefinition_AsSystemPrompt(t *testing.T) {
	def := &WorkspaceDefinition{
		Name:    "api-migration",
		Purpose: "Migrate v1 API to v2 endpoints",
		Scope:   []string{"gateway", "auth-service"},
		Tags:    []string{"go", "rest-api", "migration"},
	}

	prompt := def.AsSystemPrompt()

	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if !contains(prompt, "**Name:** api-migration") {
		t.Errorf("prompt missing name: %s", prompt)
	}
	if !contains(prompt, "**Purpose:** Migrate v1 API to v2 endpoints") {
		t.Errorf("prompt missing purpose: %s", prompt)
	}
	if !contains(prompt, "workspace:") {
		t.Errorf("prompt missing workspace tag instruction: %s", prompt)
	}
}

func TestWorkspaceDefinition_AsSystemPrompt_Empty(t *testing.T) {
	var def *WorkspaceDefinition
	prompt := def.AsSystemPrompt()
	if prompt != "" {
		t.Errorf("expected empty string for nil definition, got '%s'", prompt)
	}

	def = &WorkspaceDefinition{}
	prompt = def.AsSystemPrompt()
	if prompt != "" {
		t.Errorf("expected empty string for zero-value definition, got '%s'", prompt)
	}
}

func TestWorkspaceDefinition_ToMap_RoundTrip(t *testing.T) {
	original := &WorkspaceDefinition{
		Name:    "roundtrip-test",
		Purpose: "Test round-trip serialization",
		Scope:   []string{"test-a", "test-b"},
		Tags:    []string{"tag1", "tag2", "tag3"},
	}

	m := original.ToMap()
	recovered := WorkspaceDefinitionFromMap(m)

	if recovered.Name != original.Name {
		t.Errorf("name mismatch: expected '%s', got '%s'", original.Name, recovered.Name)
	}
	if recovered.Purpose != original.Purpose {
		t.Errorf("purpose mismatch: expected '%s', got '%s'", original.Purpose, recovered.Purpose)
	}
	if len(recovered.Scope) != len(original.Scope) {
		t.Errorf("scope length mismatch: expected %d, got %d", len(original.Scope), len(recovered.Scope))
	}
	if len(recovered.Tags) != len(original.Tags) {
		t.Errorf("tags length mismatch: expected %d, got %d", len(original.Tags), len(recovered.Tags))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
