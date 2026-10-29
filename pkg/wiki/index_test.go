package wiki

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildIndex(t *testing.T) {
	dir := t.TempDir()

	// Create test entries via the engine
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	now := time.Now().UTC()
	_ = engine.Create(&WikiEntry{Title: "Alpha", Type: EntryTypeEntity, Updated: now.Add(-2 * 24 * time.Hour)})
	_ = engine.Create(&WikiEntry{Title: "Beta", Type: EntryTypeTrend, Tags: []string{"mcp"}, Updated: now})

	entries, err := BuildIndex(dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Newest first
	if entries[0].Page != "beta-trend" || entries[1].Page != "alpha-entity" {
		t.Errorf("unexpected order: %v", entries)
	}
}

func TestWriteIndex(t *testing.T) {
	dir := t.TempDir()

	entries := []IndexEntry{
		{Page: "test-entry", Type: EntryTypeEntity, Tags: []string{"api"}, Updated: "2026-10-13"},
	}

	if err := WriteIndex(dir, entries); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	content := string(data)
	if !contains(content, "| test-entry |") {
		t.Errorf("expected table row for test-entry, got:\n%s", content)
	}
	if !contains(content, "Content Catalog") {
		t.Error("expected title in index.md")
	}
}

func TestWriteIndexEmpty(t *testing.T) {
	dir := t.TempDir()

	if err := WriteIndex(dir, nil); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !contains(string(data), "No entries yet") {
		t.Error("expected 'No entries yet' for empty index")
	}
}
