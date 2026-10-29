package wiki

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	got, err := engine.Get("test-entry-entity")
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

	if err := engine.Update("updatable-source", map[string]interface{}{"content": "updated body"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := engine.Get("updatable-source")
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

	if err := engine.Delete("todelete-task"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = engine.Get("todelete-task")
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

	_ = engine.Create(&WikiEntry{Title: "Alpha", Type: EntryTypeEntity, Content: "a"})
	_ = engine.Create(&WikiEntry{Title: "Beta", Type: EntryTypeTrend, Content: "b"})
	_ = engine.Create(&WikiEntry{Title: "Gamma", Type: EntryTypeTask, Content: "c"})

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

	_ = engine.Create(&WikiEntry{Title: "A", Type: EntryTypeEntity, Tags: []string{"x", "y"}})
	_ = engine.Create(&WikiEntry{Title: "B", Type: EntryTypeEntity, Tags: []string{"x"}})
	_ = engine.Create(&WikiEntry{Title: "C", Type: EntryTypeEntity})

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
	_ = engine.Create(&WikiEntry{Title: "Old Trend", Type: EntryTypeTrend, Updated: oldTrend})
	_ = engine.Create(&WikiEntry{Title: "Recent Trend", Type: EntryTypeTrend, Updated: now})

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

	_ = engine.Create(&WikiEntry{Title: "E1", Type: EntryTypeEntity, Content: "x"})
	_ = engine.Create(&WikiEntry{Title: "E2", Type: EntryTypeEntity, Content: "y"})
	_ = engine.Create(&WikiEntry{Title: "S1", Type: EntryTypeSource, Content: "z"})

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
	_ = engine.Create(&WikiEntry{Title: "Stale", Type: EntryTypeTrend, Updated: now.Add(-31 * 24 * time.Hour)})
	_ = engine.Create(&WikiEntry{Title: "Fresh", Type: EntryTypeTrend, Updated: now})

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

func TestEngineWriteSchema(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	err = engine.WriteSchema("test-schema", "# Schema\n\nThis is a test schema.")
	if err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	// Verify file exists and has correct frontmatter
	data, err := os.ReadFile(filepath.Join(dir, "test-schema.md"))
	if err != nil {
		t.Fatalf("read schema file: %v", err)
	}
	if !strings.Contains(string(data), "---") {
		t.Errorf("expected frontmatter delimiter in schema file")
	}
	if !strings.Contains(string(data), "type: schema") {
		t.Errorf("expected type: schema in frontmatter")
	}
}

func TestEngineReadSchema(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	content := "# My Schema\n\nThis is the schema body content."
	err = engine.WriteSchema("my-schema", content)
	if err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	readContent, err := engine.ReadSchema("my-schema")
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if readContent != content {
		t.Errorf("expected '%s', got '%s'", content, readContent)
	}
}

func TestEngineReadSchemaNotFound(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = engine.ReadSchema("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent schema")
	}
}

func TestEngineGitDiff(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Initialize a git repo in the test dir
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Skipf("git init failed: %v", err)
	}

	err = engine.WriteSchema("diff-test", "# Diff Test\n\ncontent here")
	if err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	// Stage the file
	cmd = exec.Command("git", "add", "diff-test.md")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}

	diff, err := engine.GitDiff("diff-test")
	if err != nil {
		t.Fatalf("GitDiff: %v", err)
	}
	if len(diff) == 0 {
		t.Error("expected non-empty diff output")
	}
}

func TestEngineGitCommit(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Initialize a git repo in the test dir
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Skipf("git init failed: %v", err)
	}

	// Configure git user so commits work (required in CI envs)
	cmd = exec.Command("git", "config", "user.name", "Test Bot")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Skipf("git config user.name failed: %v", err)
	}
	cmd = exec.Command("git", "config", "user.email", "test@norman.dev")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Skipf("git config user.email failed: %v", err)
	}

	err = engine.WriteSchema("commit-test", "# Commit Test\n\ncontent")
	if err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	err = engine.GitCommit("test commit message")
	if err != nil {
		t.Fatalf("GitCommit: %v", err)
	}

	// Verify commit exists via git log
	cmd = exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "test commit message") {
		t.Errorf("expected commit message in git log, got: %s", string(out))
	}
}
