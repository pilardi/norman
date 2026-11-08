package skills

import (
	"testing"
)

func TestSkillStateValid(t *testing.T) {
	if !SkillActive.Valid() {
		t.Error("expected SkillActive to be valid")
	}
	if !SkillDeactivated.Valid() {
		t.Error("expected SkillDeactivated to be valid")
	}
	if SkillState("invalid").Valid() {
		t.Error("expected invalid state to not be valid")
	}
}

func TestActivatorActivateDeactivate(t *testing.T) {
	reg := map[string]SkillEntry{
		"skill-a": {Name: "skill-a", Description: "Test skill A"},
		"skill-b": {Name: "skill-b", Description: "Test skill B"},
	}

	a := NewActivator(reg, 3)

	// Activate skill-a
	_, err := a.Activate("skill-a", nil)
	if err != nil {
		t.Fatalf("activate skill-a: %v", err)
	}

	// Verify state
	state, ok := a.GetState("skill-a")
	if !ok || state != SkillActive {
		t.Errorf("expected skill-a to be active, got state=%v ok=%v", state, ok)
	}

	// Activate skill-b
	_, err = a.Activate("skill-b", nil)
	if err != nil {
		t.Fatalf("activate skill-b: %v", err)
	}

	state, ok = a.GetState("skill-b")
	if !ok || state != SkillActive {
		t.Errorf("expected skill-b to be active, got state=%v ok=%v", state, ok)
	}

	// Deactivate skill-a
	err = a.Deactivate("skill-a")
	if err != nil {
		t.Fatalf("deactivate skill-a: %v", err)
	}

	_, ok = a.GetState("skill-a")
	if ok {
		t.Error("expected skill-a state to be removed after deactivate")
	}

	// Active() should only return skill-b
	active := a.Active()
	if len(active) != 1 || active[0] != "skill-b" {
		t.Errorf("expected ['skill-b'], got %v", active)
	}
}

func TestActivatorDeactivateAll(t *testing.T) {
	reg := map[string]SkillEntry{
		"s1": {Name: "s1"},
		"s2": {Name: "s2"},
	}

	a := NewActivator(reg, 3)
	_, err := a.Activate("s1", nil)
	if err != nil {
		t.Fatalf("activate s1: %v", err)
	}
	_, err = a.Activate("s2", nil)
	if err != nil {
		t.Fatalf("activate s2: %v", err)
	}

	a.DeactivateAll()
	active := a.Active()
	if len(active) != 0 {
		t.Errorf("expected no active skills after DeactivateAll, got %v", active)
	}
}

func TestActivatorMaxCapacity(t *testing.T) {
	reg := map[string]SkillEntry{
		"s1": {Name: "s1"},
		"s2": {Name: "s2"},
		"s3": {Name: "s3"},
		"s4": {Name: "s4"},
	}

	a := NewActivator(reg, 2) // max 2 active

	_, err := a.Activate("s1", nil)
	if err != nil {
		t.Fatalf("activate s1: %v", err)
	}
	_, err = a.Activate("s2", nil)
	if err != nil {
		t.Fatalf("activate s2: %v", err)
	}

	// Activating s3 should evict s1 (first inserted)
	_, err = a.Activate("s3", nil)
	if err != nil {
		t.Fatalf("activate s3: %v", err)
	}

	active := a.Active()
	if len(active) != 2 {
		t.Errorf("expected 2 active, got %d", len(active))
	}

	// s1 should have been evicted, s2 and s3 should remain
	state1, ok := a.GetState("s1")
	if ok && state1 == SkillActive {
		t.Error("expected s1 to be evicted")
	}

	state2, ok := a.GetState("s2")
	if !ok || state2 != SkillActive {
		t.Errorf("expected s2 to still be active")
	}

	state3, ok := a.GetState("s3")
	if !ok || state3 != SkillActive {
		t.Errorf("expected s3 to be active")
	}
}

func TestActivatorIdempotentActivate(t *testing.T) {
	reg := map[string]SkillEntry{
		"skill-x": {Name: "skill-x"},
	}

	a := NewActivator(reg, 3)

	injected1, _ := a.Activate("skill-x", nil)
	if injected1 == "" {
		t.Error("first Activate should return injection string")
	}

	injected2, _ := a.Activate("skill-x", nil)
	if injected2 != "" {
		t.Errorf("second Activate should return empty string for already-active skill, got %q", injected2)
	}
}

func TestMatchSkillWithWorkspace(t *testing.T) {
	entry := SkillEntry{
		Name:        "api-gateway",
		Description: "Manage API gateway configurations and routing rules",
		Activation:  "manual",
	}

	wsCtx := &WorkspaceContext{
		Purpose: "Migrate legacy REST APIs to gRPC gateway",
		Scope:   []string{"payments-service", "auth-service"},
		Tags:    []string{"api", "migration", "grpc"},
	}

	// Should match because "api" tag matches skill name/description
	if !MatchSkillWithWorkspace(entry, nil, wsCtx) {
		t.Error("expected match on workspace tags")
	}

	// Should match on scope item "gateway" (appears in skill name)
	wsCtx2 := &WorkspaceContext{
		Scope: []string{"api-gateway", "other"},
		Tags:  []string{},
	}
	if !MatchSkillWithWorkspace(entry, nil, wsCtx2) {
		t.Error("expected match on workspace scope containing 'gateway'")
	}

	// Should NOT match with empty context and no keywords
	wsCtx3 := &WorkspaceContext{
		Purpose: "Database migrations",
		Scope:   []string{"postgres", "redis"},
		Tags:    []string{"db", "cache"},
	}
	if MatchSkillWithWorkspace(entry, nil, wsCtx3) {
		t.Error("expected no match for unrelated workspace")
	}

	// Should match on keyword even without workspace context
	if !MatchSkillWithWorkspace(entry, []string{"gateway"}, nil) {
		t.Error("expected match on keywords")
	}

	// Auto-activation should always return true
	entry.Activation = "auto"
	if !MatchSkillWithWorkspace(entry, nil, wsCtx) {
		t.Error("expected auto skills to always match")
	}
}

func TestMatchSkillWithWorkspaceNilContext(t *testing.T) {
	entry := SkillEntry{
		Name:        "file-editor",
		Description: "Edit and modify files in the workspace",
		Activation:  "manual",
	}

	// With nil context, should behave like MatchSkill (keyword-only matching)
	if !MatchSkillWithWorkspace(entry, []string{"edit"}, nil) {
		t.Error("expected match on keywords with nil context")
	}

	if MatchSkillWithWorkspace(entry, []string{"database"}, nil) {
		t.Error("expected no match for unrelated keyword")
	}
}

func TestMatchSkillWithWorkspaceEmptyContext(t *testing.T) {
	entry := SkillEntry{
		Name:        "test-runner",
		Description: "Run unit and integration tests",
		Activation:  "manual",
	}

	wsCtx := &WorkspaceContext{
		Purpose: "",
		Scope:   []string{},
		Tags:    []string{},
	}

	// Empty context should not trigger matches beyond keywords
	if MatchSkillWithWorkspace(entry, nil, wsCtx) {
		t.Error("expected no match with empty workspace context")
	}

	if !MatchSkillWithWorkspace(entry, []string{"test"}, wsCtx) {
		t.Error("expected match on keyword 'test' even with empty context")
	}
}

func TestMatchSkillWithWorkspacePurposeMatch(t *testing.T) {
	entry := SkillEntry{
		Name:        "grpc-client",
		Description: "Generate gRPC client stubs and manage service connections",
		Activation:  "manual",
	}

	wsCtx := &WorkspaceContext{
		Purpose: "Migrate legacy REST APIs to gRPC gateway",
		Scope:   []string{},
		Tags:    []string{},
	}

	// Should match because "grpc" appears in purpose
	if !MatchSkillWithWorkspace(entry, nil, wsCtx) {
		t.Error("expected match on workspace purpose containing 'grpc'")
	}
}
