package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistTurn(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist a turn
	turn := map[string]interface{}{
		"role":    "assistant",
		"content": "Hello from tool call",
		"id":      "call_abc123",
	}

	err = store.PersistTurn("task-001", turn)
	if err != nil {
		t.Fatalf("persist turn: %v", err)
	}

	// Verify file exists and is valid JSONL
	path := filepath.Join(base, "turns", "task-001.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read turns file: %v", err)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("unmarshal turn: %v", err)
	}

	if entry["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", entry["role"])
	}
	if entry["id"] != "call_abc123" {
		t.Errorf("expected id 'call_abc123', got '%v'", entry["id"])
	}
}

func TestStorePersistTurnMultiple(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist two turns sequentially
	turn1 := map[string]interface{}{"role": "user", "content": "First turn"}
	err = store.PersistTurn("task-002", turn1)
	if err != nil {
		t.Fatalf("persist turn 1: %v", err)
	}

	turn2 := map[string]interface{}{"role": "assistant", "content": "Second turn"}
	err = store.PersistTurn("task-002", turn2)
	if err != nil {
		t.Fatalf("persist turn 2: %v", err)
	}

	// Load and verify both turns are present
	turns, err := store.LoadTurns("task-002")
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}

	if len(turns) != 2 {
		t.Errorf("expected 2 turns, got %d", len(turns))
	}

	if turns[0]["role"] != "user" {
		t.Errorf("turn 1 role: expected 'user', got '%s'", turns[0]["role"])
	}
	if turns[1]["role"] != "assistant" {
		t.Errorf("turn 2 role: expected 'assistant', got '%s'", turns[1]["role"])
	}
}

func TestStorePersistTurnEmpty(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.PersistTurn("task-empty", map[string]interface{}{})
	if err != nil {
		t.Fatalf("persist empty turn: %v", err)
	}

	// Should produce a valid JSON line
	path := filepath.Join(base, "turns", "task-empty.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read turns file: %v", err)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("unmarshal empty turn: %v", err)
	}
}

func TestStoreLoadTurnsNotFound(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	turns, err := store.LoadTurns("nonexistent-task")
	if err != nil {
		t.Fatalf("load nonexistent turns should not error: %v", err)
	}
	if len(turns) != 0 {
		t.Errorf("expected empty result for missing task, got %d entries", len(turns))
	}
}

func TestStoreDeleteTurns(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Create a turns file
	turn := map[string]interface{}{"role": "assistant", "content": "test"}
	err = store.PersistTurn("task-delete", turn)
	if err != nil {
		t.Fatalf("persist turn: %v", err)
	}

	// Delete it
	err = store.DeleteTurns("task-delete")
	if err != nil {
		t.Fatalf("delete turns: %v", err)
	}

	// Verify file is gone
	path := filepath.Join(base, "turns", "task-delete.jsonl")
	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted, still exists")
	}

	// Delete again should be idempotent
	err = store.DeleteTurns("task-delete")
	if err != nil {
		t.Fatalf("delete again should not error: %v", err)
	}
}

func TestStorePersistToolCallResult(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.PersistToolCallResult("task-003", "ls", `{"path": "/tmp"}`, "file1\nfile2\n", 0)
	if err != nil {
		t.Fatalf("persist tool call result: %v", err)
	}

	path := filepath.Join(base, "tool_results", "task-003.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results file: %v", err)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if entry["name"] != "ls" {
		t.Errorf("expected name 'ls', got '%s'", entry["name"])
	}
	if entry["exit_code"].(float64) != 0 {
		t.Errorf("expected exit_code 0, got %v", entry["exit_code"])
	}
}

func TestStoreLoadToolCallResults(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist two tool call results
	err = store.PersistToolCallResult("task-004", "ls", `{"path": "/tmp"}`, "a\nb\n", 0)
	if err != nil {
		t.Fatalf("persist result 1: %v", err)
	}
	err = store.PersistToolCallResult("task-004", "cat", `{"path": "/tmp/a"}`, "content of a", 0)
	if err != nil {
		t.Fatalf("persist result 2: %v", err)
	}

	results, err := store.LoadToolCallResults("task-004")
	if err != nil {
		t.Fatalf("load results: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	if results[0]["name"] != "ls" {
		t.Errorf("result 1 name: expected 'ls', got '%s'", results[0]["name"])
	}
	if results[1]["name"] != "cat" {
		t.Errorf("result 2 name: expected 'cat', got '%s'", results[1]["name"])
	}
}

func TestStoreLoadToolCallResultsNotFound(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	results, err := store.LoadToolCallResults("nonexistent")
	if err != nil {
		t.Fatalf("load nonexistent results should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty result for missing task, got %d entries", len(results))
	}
}
