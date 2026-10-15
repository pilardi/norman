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

func TestStoreSavePlan(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.SavePlan("plan-001", "# My Plan\n\nStep 1: Do something\nStep 2: Do another thing")
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}

	// Verify file was created and is valid JSON
	path := filepath.Join(base, "plans", "plan-001.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}

	if result["id"] != "plan-001" {
		t.Errorf("expected id 'plan-001', got '%v'", result["id"])
	}

	content, ok := result["content"].(string)
	if !ok {
		t.Fatal("content should be a string")
	}
	if content != "# My Plan\n\nStep 1: Do something\nStep 2: Do another thing" {
		t.Errorf("unexpected plan content: %q", content)
	}

	// Verify created_at and updated_at are present
	if _, ok := result["created_at"].(string); !ok {
		t.Error("expected created_at field")
	}
	if _, ok := result["updated_at"].(string); !ok {
		t.Error("expected updated_at field")
	}
}

func TestStoreLoadPlan(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.SavePlan("plan-002", "plan content here")
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}

	content, err := store.LoadPlan("plan-002")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	if content != "plan content here" {
		t.Errorf("expected 'plan content here', got '%s'", content)
	}
}

func TestStoreLoadPlanNotFound(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_, err = store.LoadPlan("nonexistent-plan")
	if err == nil {
		t.Fatal("expected error for missing plan")
	}

	if !contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestStoreDeletePlan(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.SavePlan("plan-003", "content")
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}

	err = store.DeletePlan("plan-003")
	if err != nil {
		t.Fatalf("delete plan: %v", err)
	}

	// Verify file is gone
	path := filepath.Join(base, "plans", "plan-003.json")
	_, err = os.ReadFile(path)
	if !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted, got: %v", err)
	}
}

func TestStoreDeletePlanNotFound(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Deleting a non-existent plan should not error
	err = store.DeletePlan("nonexistent")
	if err != nil {
		t.Fatalf("delete nonexistent plan: %v", err)
	}
}

func TestStoreListPlans(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	store.SavePlan("plan-a", "content a")
	store.SavePlan("plan-b", "content b")
	store.SavePlan("plan-c", "content c")

	plans, err := store.ListPlans()
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}

	if len(plans) != 3 {
		t.Errorf("expected 3 plans, got %d", len(plans))
	}
}

func TestStoreStatsIncludesPlans(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	store.SaveTask("task-1", map[string]interface{}{"id": "task-1"})
	store.SaveSession("session-1", map[string]interface{}{"id": "session-1"})
	store.SavePlan("plan-1", "content")
	store.SavePlan("plan-2", "content")

	tasks, sessions, plans, err := store.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if tasks != 1 {
		t.Errorf("expected 1 task, got %d", tasks)
	}
	if sessions != 1 {
		t.Errorf("expected 1 session, got %d", sessions)
	}
	if plans != 2 {
		t.Errorf("expected 2 plans, got %d", plans)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
