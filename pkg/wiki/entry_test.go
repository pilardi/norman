package wiki

import (
	"strings"
	"testing"
	"time"
)

func TestParseFrontmatter(t *testing.T) {
	markdown := `---
title: "Project Alpha"
type: entity
tags: [api, backend]
pinned: true
updated: 2026-10-13
created_at: 2026-09-01
---

This is the body of the entry.

## Details

Some markdown content here.`

	entry, err := ParseFrontmatter(markdown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Title != "Project Alpha" {
		t.Errorf("expected title 'Project Alpha', got '%s'", entry.Title)
	}
	if entry.Type != EntryTypeEntity {
		t.Errorf("expected type entity, got %s", entry.Type)
	}
	if len(entry.Tags) != 2 || entry.Tags[0] != "api" {
		t.Errorf("unexpected tags: %v", entry.Tags)
	}
	if !entry.Pinned {
		t.Error("expected pinned=true")
	}
	if entry.Content == "" {
		t.Error("expected non-empty content body")
	}
	if entry.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
}

func TestParseFrontmatterMissingTitle(t *testing.T) {
	markdown := `---
type: trend
tags: [mcp]
---

Body text.`

	_, err := ParseFrontmatter(markdown)
	if err == nil {
		t.Error("expected error for missing title")
	}
}

func TestParseFrontmatterInvalidType(t *testing.T) {
	markdown := `---
title: "Test"
type: unknown_type
---

Body text.`

	_, err := ParseFrontmatter(markdown)
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestTitleToSlug(t *testing.T) {
	tests := []struct {
		input     string
		entryType EntryType
		expected  string
	}{
		{"Project Alpha", EntryTypeEntity, "project-alpha-entity"},
		{"API Gateway v2", EntryTypeSource, "api-gateway-v2-source"},
		{"Docker_Build_Optimization", EntryTypeTask, "docker-build-optimization-task"},
		{"Hello World!", EntryTypeTrend, "hello-world-trend"},
		{"  Mixed CASE  ", EntryTypeEntity, "mixed-case-entity"},
	}

	for _, tc := range tests {
		got := TitleToSlug(tc.input, tc.entryType)
		if got != tc.expected {
			t.Errorf("TitleToSlug(%q, %s) = %q, want %q", tc.input, tc.entryType, got, tc.expected)
		}
	}

	// Verify collision avoidance: same title, different types → different slugs
	slug1 := TitleToSlug("Docker", EntryTypeEntity)
	slug2 := TitleToSlug("Docker", EntryTypeTask)
	if slug1 == slug2 {
		t.Errorf("collision detected: %q == %q", slug1, slug2)
	}
	if !strings.HasSuffix(slug1, "-entity") || !strings.HasSuffix(slug2, "-task") {
		t.Errorf("expected type suffixes, got %q and %q", slug1, slug2)
	}
}

func TestEntryIsStale(t *testing.T) {
	now := time.Now()
	entry := &WikiEntry{
		Type:    EntryTypeTrend,
		Updated: now.Add(-31 * 24 * time.Hour),
	}
	if !entry.IsStale() {
		t.Error("expected trend entry >30 days old to be stale")
	}

	entry.Updated = now.Add(-29 * 24 * time.Hour)
	if entry.IsStale() {
		t.Error("expected trend entry <30 days old NOT to be stale")
	}

	entry.Type = EntryTypeEntity
	if entry.IsStale() {
		t.Error("expected entity type not to be stale regardless of age")
	}
}

func TestEntryHasTag(t *testing.T) {
	entry := &WikiEntry{Tags: []string{"api", "backend"}}

	if !entry.HasTag("api") {
		t.Error("expected HasTag('api') to return true")
	}
	if entry.HasTag("frontend") {
		t.Error("expected HasTag('frontend') to return false")
	}
	if !entry.HasTag("BACKEND") {
		t.Error("expected case-insensitive tag match")
	}
}

func TestEntryHasWorkspace(t *testing.T) {
	entry := &WikiEntry{Workspace: "api-migration"}
	if !entry.HasWorkspace() {
		t.Error("expected HasWorkspace() to return true for non-empty workspace")
	}

	entry.Workspace = ""
	if entry.HasWorkspace() {
		t.Error("expected HasWorkspace() to return false for empty workspace")
	}
}

func TestToMarkdown(t *testing.T) {
	entry := &WikiEntry{
		Title:     "Test Entry",
		Type:      EntryTypeSource,
		Tags:      []string{"test"},
		Pinned:    false,
		Updated:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Content:   "Some body text.",
	}

	markdown := entry.ToMarkdown()
	if markdown == "" {
		t.Error("expected non-empty markdown output")
	}
	if !contains(markdown, "---") {
		t.Error("expected frontmatter delimiters in output")
	}
	if !contains(markdown, "Some body text.") {
		t.Error("expected body content in output")
	}
}

func TestToMarkdownWithWorkspace(t *testing.T) {
	entry := &WikiEntry{
		Title:     "Test Entry",
		Type:      EntryTypeEntity,
		Tags:      []string{"test"},
		Pinned:    false,
		Workspace: "api-migration",
		Updated:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Content:   "Some body text.",
	}

	markdown := entry.ToMarkdown()
	if !contains(markdown, "workspace:") {
		t.Error("expected workspace field in markdown output")
	}
	if !contains(markdown, "api-migration") {
		t.Error("expected workspace value in markdown output")
	}
}

func TestParseFrontmatterWithWorkspace(t *testing.T) {
	markdown := `---
title: "Project Alpha"
type: entity
workspace: api-migration
tags: [api, backend]
pinned: true
updated: 2026-10-13
created_at: 2026-09-01
---

This is the body of the entry.`

	entry, err := ParseFrontmatter(markdown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Workspace != "api-migration" {
		t.Errorf("expected workspace 'api-migration', got '%s'", entry.Workspace)
	}
}

func TestParseFrontmatterRoundTripWithWorkspace(t *testing.T) {
	original := &WikiEntry{
		Title:     "Round Trip Test",
		Type:      EntryTypeTask,
		Tags:      []string{"roundtrip", "test"},
		Workspace: "test-workspace",
		Pinned:    true,
		Updated:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Content:   "Round trip body content.",
	}

	markdown := original.ToMarkdown()
	parsed, err := ParseFrontmatter(markdown)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}

	if parsed.Workspace != original.Workspace {
		t.Errorf("workspace mismatch: expected '%s', got '%s'", original.Workspace, parsed.Workspace)
	}
	if parsed.Title != original.Title {
		t.Errorf("title mismatch: expected '%s', got '%s'", original.Title, parsed.Title)
	}
	if len(parsed.Tags) != len(original.Tags) {
		t.Errorf("tags length mismatch: expected %d, got %d", len(original.Tags), len(parsed.Tags))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
