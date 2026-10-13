package memory

import (
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

	// Verify file was created
	path := filepath.Join(base, "sessions", "task-001.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	lines := len(string(data))
	if lines < 40 {
		t.Errorf("expected substantial content, got %d bytes", lines)
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

	// Second archival (should append)
	turns2 := []interface{}{map[string]interface{}{"role": "assistant", "content": "Second"}}
	store.ArchiveSession("task-002", turns2)

	path := filepath.Join(base, "sessions", "task-002.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	// Should have two lines (two archival calls appended)
	content := string(data)
	lineCount := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lineCount++
		}
	}
	if lineCount != 2 {
		t.Errorf("expected 2 lines, got %d", lineCount)
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

	path := filepath.Join(base, "sessions", "task-empty.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}
