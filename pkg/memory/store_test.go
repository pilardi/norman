package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	// Verify archive file was created and is valid JSON (.archive.json extension)
	path := filepath.Join(base, "sessions", "task-001.archive.json")
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
	_ = store.ArchiveSession("task-002", turns1)

	// Second archival (should overwrite previous)
	turns2 := []interface{}{map[string]interface{}{"role": "assistant", "content": "Second"}}
	_ = store.ArchiveSession("task-002", turns2)

	// Verify archive file was created (.archive.json extension)
	path := filepath.Join(base, "sessions", "task-002.archive.json")
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

	// Verify archive file was created (.archive.json extension)
	path := filepath.Join(base, "sessions", "task-empty.archive.json")
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

	_ = store.SavePlan("plan-a", "content a")
	_ = store.SavePlan("plan-b", "content b")
	_ = store.SavePlan("plan-c", "content c")

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

	_ = store.SaveTask("task-1", map[string]interface{}{"id": "task-1"})
	_ = store.SaveSession("session-1", map[string]interface{}{"id": "session-1"})
	_ = store.SavePlan("plan-1", "content")
	_ = store.SavePlan("plan-2", "content")

	tasks, sessions, plans, _, err := store.Stats()
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

func TestStoreStatsIncludesWorkspaces(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_ = store.SaveTask("task-1", map[string]interface{}{"id": "task-1"})
	_ = store.SaveSession("session-1", map[string]interface{}{"id": "session-1"})
	_ = store.SavePlan("plan-1", "content")
	_ = store.SaveWorkspace("ws-alpha", map[string]interface{}{"name": "ws-alpha", "path": "/tmp/ws"})
	_ = store.SaveWorkspace("ws-beta", map[string]interface{}{"name": "ws-beta", "path": "/tmp/ws2"})

	tasks, sessions, plans, workspaces, err := store.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if tasks != 1 {
		t.Errorf("expected 1 task, got %d", tasks)
	}
	if sessions != 1 {
		t.Errorf("expected 1 session, got %d", sessions)
	}
	if plans != 1 {
		t.Errorf("expected 1 plan, got %d", plans)
	}
	if workspaces != 2 {
		t.Errorf("expected 2 workspaces, got %d", workspaces)
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

func TestStorePersistSessionTurn(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist a user turn
	err = store.PersistSessionTurn("sess-001", map[string]interface{}{
		"role":    "user",
		"content": "Hello, how are you?",
	})
	if err != nil {
		t.Fatalf("persist session turn: %v", err)
	}

	// Persist an assistant turn
	err = store.PersistSessionTurn("sess-001", map[string]interface{}{
		"role":    "assistant",
		"content": "I'm doing well, thanks!",
	})
	if err != nil {
		t.Fatalf("persist session turn: %v", err)
	}

	// Verify the turns file exists and is valid JSONL
	path := filepath.Join(base, "turns", "sess-001.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session turns file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entries []map[string]interface{}
	for _, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal turn: %v", err)
		}
		entries = append(entries, entry)
	}

	if entries[0]["role"] != "user" {
		t.Errorf("first entry role expected 'user', got '%s'", entries[0]["role"])
	}
	if entries[1]["role"] != "assistant" {
		t.Errorf("second entry role expected 'assistant', got '%s'", entries[1]["role"])
	}
}

func TestStoreLoadSessionTurns(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist multiple turns
	turns := []map[string]interface{}{
		{"role": "user", "content": "First question"},
		{"role": "assistant", "content": "Answer 1"},
		{"role": "user", "content": "Follow-up"},
	}
	for _, turn := range turns {
		if err := store.PersistSessionTurn("sess-002", turn); err != nil {
			t.Fatalf("persist turn: %v", err)
		}
	}

	// Load them back
	loaded, err := store.LoadSessionTurns("sess-002")
	if err != nil {
		t.Fatalf("load session turns: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(loaded))
	}

	if loaded[0]["role"] != "user" || loaded[2]["role"] != "user" {
		t.Errorf("unexpected role sequence")
	}
}

func TestStoreLoadSessionTurnsEmpty(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Load turns for a session that has none
	result, err := store.LoadSessionTurns("sess-nonexistent")
	if err != nil {
		t.Fatalf("load nonexistent session turns: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty session, got %d entries", len(result))
	}
}

func TestStorePersistSessionTurnMultipleSessions(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist turns for two different sessions independently
	err = store.PersistSessionTurn("sess-a", map[string]interface{}{"role": "user", "content": "A msg"})
	if err != nil {
		t.Fatalf("persist sess-a turn: %v", err)
	}
	err = store.PersistSessionTurn("sess-b", map[string]interface{}{"role": "assistant", "content": "B reply"})
	if err != nil {
		t.Fatalf("persist sess-b turn: %v", err)
	}

	// Verify each session has its own file
	dataA, _ := os.ReadFile(filepath.Join(base, "turns", "sess-a.jsonl"))
	dataB, _ := os.ReadFile(filepath.Join(base, "turns", "sess-b.jsonl"))

	if !strings.Contains(string(dataA), "A msg") {
		t.Errorf("sess-a should contain 'A msg'")
	}
	if !strings.Contains(string(dataB), "B reply") {
		t.Errorf("sess-b should contain 'B reply'")
	}
}

func TestStorePersistSessionTurnWithID(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Persist a turn with an explicit ID field
	err = store.PersistSessionTurn("sess-003", map[string]interface{}{
		"id":      "turn-abc123",
		"role":    "user",
		"content": "Message with ID",
	})
	if err != nil {
		t.Fatalf("persist session turn: %v", err)
	}

	loaded, err := store.LoadSessionTurns("sess-003")
	if err != nil {
		t.Fatalf("load session turns: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(loaded))
	}
	if loaded[0]["id"] != "turn-abc123" {
		t.Errorf("expected id 'turn-abc123', got '%v'", loaded[0]["id"])
	}
}

func TestStorePersistSessionTurnTimestamp(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = store.PersistSessionTurn("sess-ts", map[string]interface{}{
		"role":    "user",
		"content": "Timestamp test",
	})
	if err != nil {
		t.Fatalf("persist session turn: %v", err)
	}

	loaded, err := store.LoadSessionTurns("sess-ts")
	if err != nil {
		t.Fatalf("load session turns: %v", err)
	}

	ts, ok := loaded[0]["timestamp"].(string)
	if !ok {
		t.Fatal("expected timestamp field as string")
	}
	if ts == "" {
		t.Error("timestamp should not be empty")
	}
}

func TestStoreArchiveSessionHasArchiveStatus(t *testing.T) {
	base := t.TempDir()
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	turns := []interface{}{
		map[string]interface{}{"role": "user", "content": "Hello"},
		map[string]interface{}{"role": "assistant", "content": "Hi there!"},
	}

	err = store.ArchiveSession("task-archived", turns)
	if err != nil {
		t.Fatalf("archive session: %v", err)
	}

	// Verify archive file contains archive_status field
	path := filepath.Join(base, "sessions", "task-archived.archive.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}

	for i, entry := range entries {
		status, ok := entry["archive_status"].(string)
		if !ok {
			t.Errorf("entry %d missing archive_status field", i)
		} else if status != "archived" {
			t.Errorf("entry %d archive_status = %q, want 'archived'", i, status)
		}

		_, hasArchivedAt := entry["archived_at"]
		if !hasArchivedAt {
			t.Errorf("entry %d missing archived_at field", i)
		}
	}
}

func TestIsSessionArchived(t *testing.T) {
	session := map[string]interface{}{
		"id":             "sess-001",
		"status":         "active",
		"archive_status": "archived",
	}

	if !IsSessionArchived(session) {
		t.Error("expected session with archive_status='archived' to be archived")
	}

	session2 := map[string]interface{}{
		"id":     "sess-002",
		"status": "active",
	}
	if IsSessionArchived(session2) {
		t.Error("expected session without archive_status to not be archived")
	}
}

func TestSessionArchiveStatus(t *testing.T) {
	session := map[string]interface{}{
		"id":             "sess-001",
		"archive_status": "archived",
	}

	status := SessionArchiveStatus(session)
	if status != ArchiveArchived {
		t.Errorf("expected ArchiveArchived, got %q", status)
	}

	session2 := map[string]interface{}{
		"id": "sess-002",
	}
	status = SessionArchiveStatus(session2)
	if status != ArchiveNone {
		t.Errorf("expected ArchiveNone, got %q", status)
	}
}
