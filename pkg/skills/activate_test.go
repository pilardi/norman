package skills

import (
	"testing"
)

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
