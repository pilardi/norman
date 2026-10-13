package wiki

import (
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
		input    string
		expected string
	}{
		{"Project Alpha", "project-alpha"},
		{"API Gateway v2", "api-gateway-v2"},
		{"Docker_Build_Optimization", "docker-build-optimization"},
		{"Hello World!", "hello-world"},
		{"  Mixed CASE  ", "mixed-case"},
	}

	for _, tc := range tests {
		got := TitleToSlug(tc.input)
		if got != tc.expected {
			t.Errorf("TitleToSlug(%q) = %q, want %q", tc.input, got, tc.expected)
		}
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
