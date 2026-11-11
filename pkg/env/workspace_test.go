package env

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

// --- Workspace interface method tests (self-contained mocks) ---

// mockWorkspace implements the Workspace interface for testing without requiring OpenShell.
type mockWorkspace struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func newMockWorkspace() *mockWorkspace {
	return &mockWorkspace{sessions: make(map[string]*Session)}
}

func (m *mockWorkspace) SessionWorkspace(sessionId string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionId]
	if !ok {
		return "", fmt.Errorf("session %s not found", sessionId)
	}
	return s.sessionId, nil
}

func (m *mockWorkspace) Get(ctx context.Context, sessionId string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionId]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionId)
	}
	return s, nil
}

func (m *mockWorkspace) Delete(ctx context.Context, sessionId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionId)
	return nil
}

func (m *mockWorkspace) UpdateActivity(ctx context.Context, sessionId string) {}

func (m *mockWorkspace) Sessions() map[string]*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]*Session, len(m.sessions))
	for k, v := range m.sessions {
		result[k] = v
	}
	return result
}

func TestWorkspaceUpdateActivity(t *testing.T) {
	ws := newMockWorkspace()
	ctx := context.Background()

	// Pre-populate a session (Create is internal-only for setup)
	ws.sessions["test-session-001"] = &Session{sessionId: "test-session-001"}

	// UpdateActivity should not panic or error for a valid session ID
	ws.UpdateActivity(ctx, "test-session-001")

	// Verify the session still exists and is accessible
	session, err := ws.Get(ctx, "test-session-001")
	if err != nil || session == nil {
		t.Error("expected session to still exist after UpdateActivity")
	}
}

func TestWorkspaceSessions(t *testing.T) {
	ws := newMockWorkspace()
	ctx := context.Background()

	// Pre-populate sessions (Create is internal-only for setup)
	for i := 1; i <= 3; i++ {
		sid := fmt.Sprintf("session-%03d", i)
		ws.sessions[sid] = &Session{sessionId: sid}
	}

	// Sessions() should return all created sessions
	sessions := ws.Sessions()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}

	// Verify each session ID is present
	for i := 1; i <= 3; i++ {
		sid := fmt.Sprintf("session-%03d", i)
		if _, ok := sessions[sid]; !ok {
			t.Errorf("expected session '%s' to be in Sessions() result", sid)
		}
	}

	// Delete one and verify count drops
	err := ws.Delete(ctx, "session-002")
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}

	sessions = ws.Sessions()
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions after delete, got %d", len(sessions))
	}
}

func TestWorkspaceGetNotFound(t *testing.T) {
	ws := newMockWorkspace()
	ctx := context.Background()

	// Get a non-existent session should return NotFound error
	_, err := ws.Get(ctx, "nonexistent-session")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %v", err)
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
