package wiki

import (
	"testing"
	"time"
)

func TestEngineCreateAndGet(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	entry := &WikiEntry{
		Title:     "Test Entry",
		Type:      EntryTypeEntity,
		Tags:      []string{"test"},
		Pinned:    false,
		Updated:   time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
		Content:   "Hello world.",
	}

	if err := engine.Create(entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := engine.Get("test-entry")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Test Entry" {
		t.Errorf("expected title 'Test Entry', got '%s'", got.Title)
	}
	if got.Content != "Hello world." {
		t.Errorf("unexpected content: %q", got.Content)
	}
}

func TestEngineCreateDuplicate(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	entry := &WikiEntry{Title: "Dup", Type: EntryTypeEntity, Content: "first"}
	if err := engine.Create(entry); err != nil {
		t.Fatalf("Create first: %v", err)
	}

	err = engine.Create(&WikiEntry{Title: "Dup", Type: EntryTypeEntity, Content: "second"})
	if err == nil {
		t.Error("expected error on duplicate create")
	}
}

func TestEngineUpdate(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	entry := &WikiEntry{Title: "Updatable", Type: EntryTypeSource, Content: "original"}
	if err := engine.Create(entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := engine.Update("updatable", map[string]interface{}{"content": "updated body"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := engine.Get("updatable")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Content != "updated body" {
		t.Errorf("expected 'updated body', got '%s'", got.Content)
	}
}

func TestEngineDelete(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	entry := &WikiEntry{Title: "ToDelete", Type: EntryTypeTask, Content: "will be gone"}
	if err := engine.Create(entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := engine.Delete("todelete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = engine.Get("todelete")
	if err == nil {
		t.Error("expected ErrNotFound after delete")
	}
}

func TestEngineList(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	engine.Create(&WikiEntry{Title: "Alpha", Type: EntryTypeEntity, Content: "a"})
	engine.Create(&WikiEntry{Title: "Beta", Type: EntryTypeTrend, Content: "b"})
	engine.Create(&WikiEntry{Title: "Gamma", Type: EntryTypeTask, Content: "c"})

	all, err := engine.List(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 entries, got %d", len(all))
	}

	filtered, err := engine.List(&Filter{Types: []EntryType{EntryTypeEntity}})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Title != "Alpha" {
		t.Errorf("expected only Alpha, got %v", filtered)
	}
}

func TestEngineListWithTags(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	engine.Create(&WikiEntry{Title: "A", Type: EntryTypeEntity, Tags: []string{"x", "y"}})
	engine.Create(&WikiEntry{Title: "B", Type: EntryTypeEntity, Tags: []string{"x"}})
	engine.Create(&WikiEntry{Title: "C", Type: EntryTypeEntity})

	filtered, err := engine.List(&Filter{Tags: []string{"x"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 entries with tag x, got %d", len(filtered))
	}
}

func TestEnginePruneStale(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	now := time.Now().UTC()
	oldTrend := now.Add(-31 * 24 * time.Hour)
	engine.Create(&WikiEntry{Title: "Old Trend", Type: EntryTypeTrend, Updated: oldTrend})
	engine.Create(&WikiEntry{Title: "Recent Trend", Type: EntryTypeTrend, Updated: now})

	removed := engine.PruneStale()
	if removed != 1 {
		t.Errorf("expected 1 removal, got %d", removed)
	}

	stale, err := engine.ListStale()
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected no stale entries after prune, got %d", len(stale))
	}
}

func TestEngineStats(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	engine.Create(&WikiEntry{Title: "E1", Type: EntryTypeEntity, Content: "x"})
	engine.Create(&WikiEntry{Title: "E2", Type: EntryTypeEntity, Content: "y"})
	engine.Create(&WikiEntry{Title: "S1", Type: EntryTypeSource, Content: "z"})

	stats := engine.Stats()
	if stats[EntryTypeEntity] != 2 {
		t.Errorf("expected 2 entities, got %d", stats[EntryTypeEntity])
	}
	if stats[EntryTypeSource] != 1 {
		t.Errorf("expected 1 source, got %d", stats[EntryTypeSource])
	}
}

func TestEngineDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	err = engine.Delete("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent slug")
	}
}

func TestFilterStale(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	now := time.Now().UTC()
	engine.Create(&WikiEntry{Title: "Stale", Type: EntryTypeTrend, Updated: now.Add(-31 * 24 * time.Hour)})
	engine.Create(&WikiEntry{Title: "Fresh", Type: EntryTypeTrend, Updated: now})

	staleOnly, err := engine.List(&Filter{Stale: true})
	if err != nil {
		t.Fatalf("List stale: %v", err)
	}
	if len(staleOnly) != 1 || staleOnly[0].Title != "Stale" {
		t.Errorf("expected only Stale, got %v", staleOnly)
	}

	freshOnly, err := engine.List(&Filter{Stale: false})
	if err != nil {
		t.Fatalf("List fresh: %v", err)
	}
	if len(freshOnly) != 1 || freshOnly[0].Title != "Fresh" {
		t.Errorf("expected only Fresh, got %v", freshOnly)
	}
}
