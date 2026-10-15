package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreArchiveSession(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	turns := []interface{}{
		map[string]interface{}{"role": "user", "content": "Hello"},
		map[string]interface{}{"role": "assistant", "content": "Hi there!"},
	}

	err = store.ArchiveSession("task-001", turns)
	if err != nil {
		t.Fatalf("archive session: %v", err)
	}

	// Verify file was created and is valid JSON
	path := filepath.Join(base, "sessions", "task-001.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestStoreArchiveSessionMultiple(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// First archival
	turns1 := []interface{}{map[string]interface{}{"role": "user", "content": "First"}}
	store.ArchiveSession("task-002", turns1)

	// Second archival (should overwrite previous)
	turns2 := []interface{}{map[string]interface{}{"role": "assistant", "content": "Second"}}
	store.ArchiveSession("task-002", turns2)

	path := filepath.Join(base, "sessions", "task-002.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	// Should have one entry (second call overwrites first)
	var result []map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 entry (overwrite), got %d", len(result))
	}
	if result[0]["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", result[0]["role"])
	}
}

func TestStoreArchiveSessionEmpty(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.ArchiveSession("task-empty", nil)
	if err != nil {
		t.Fatalf("archive empty: %v", err)
	}

	path := filepath.Join(base, "sessions", "task-empty.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	// Empty turns should produce a valid empty JSON array
	var result []map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal empty archive: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result))
	}
}
