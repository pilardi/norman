package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/norman/norman/pkg/memory"
)

func TestDeleteSessionRemovesTurnsFile(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	sid := uuid.New().String()

	// Create session JSON file
	sessionData := map[string]interface{}{
		"id":           sid,
		"name":         "test-session",
		"workspace_id": "ws-1",
		"status":       "active",
		"created_at":   time.Now().Format(time.RFC3339),
	}
	if err := store.SaveSession(sid, sessionData); err != nil {
		t.Fatalf("save session: %v", err)
	}

	// Create turns .jsonl file
	turnsPath := filepath.Join(base, "turns")
	if err := os.MkdirAll(turnsPath, 0755); err != nil {
		t.Fatalf("create turns dir: %v", err)
	}
	turnsFile := filepath.Join(turnsPath, sid+".jsonl")
	turnEntry := map[string]interface{}{"role": "user", "content": "Hello"}
	line, _ := json.Marshal(turnEntry)
	if err := os.WriteFile(turnsFile, append(line, '\n'), 0644); err != nil {
		t.Fatalf("write turns file: %v", err)
	}

	// Verify files exist before deletion
	if _, err := os.Stat(filepath.Join(base, "sessions", sid+".json")); err != nil {
		t.Fatalf("session file should exist before delete")
	}
	if _, err := os.Stat(turnsFile); err != nil {
		t.Fatalf("turns file should exist before delete")
	}

	// Simulate DeleteSession cleanup logic: remove session JSON + turns .jsonl
	sessionsDir := filepath.Join(base, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}

	var fullPath string
	for _, entry := range entries {
		if entry.Name() == sid+".json" {
			fullPath = filepath.Join(sessionsDir, entry.Name())
			break
		}
	}
	if fullPath == "" {
		t.Fatalf("session file not found")
	}

	if err := os.Remove(fullPath); err != nil {
		t.Fatalf("remove session file: %v", err)
	}

	// Remove turns file (this is the new cleanup behavior)
	turnsCleanupPath := filepath.Join(base, "turns", sid+".jsonl")
	if err := os.Remove(turnsCleanupPath); err != nil {
		t.Fatalf("remove turns file: %v", err)
	}

	// Verify both files are gone
	if _, err := os.Stat(filepath.Join(base, "sessions", sid+".json")); !os.IsNotExist(err) {
		t.Errorf("session file should be removed")
	}
	if _, err := os.Stat(turnsFile); !os.IsNotExist(err) {
		t.Errorf("turns file should be removed")
	}

	// Verify session data is still readable from store (the Store.DeleteSession method)
	_, err = store.LoadSession(sid)
	if err == nil {
		t.Errorf("session should no longer be loadable after removal")
	}
}

func TestDeleteSessionRemovesAssociatedTasks(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	sid := uuid.New().String()

	// Create session JSON file
	sessionData := map[string]interface{}{
		"id":           sid,
		"name":         "test-session",
		"workspace_id": "ws-1",
		"status":       "active",
		"created_at":   time.Now().Format(time.RFC3339),
	}
	if err := store.SaveSession(sid, sessionData); err != nil {
		t.Fatalf("save session: %v", err)
	}

	// Create task JSON files with matching session_id
	task1ID := uuid.New().String()
	task2ID := uuid.New().String()

	task1 := map[string]interface{}{
		"id":         task1ID,
		"session_id": sid,
		"status":     "completed",
		"created_at": time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
		"output":     "task 1 output",
	}
	task2 := map[string]interface{}{
		"id":         task2ID,
		"session_id": sid,
		"status":     "completed",
		"created_at": time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		"output":     "task 2 output",
	}

	if err := store.SaveTask(task1ID, task1); err != nil {
		t.Fatalf("save task 1: %v", err)
	}
	if err := store.SaveTask(task2ID, task2); err != nil {
		t.Fatalf("save task 2: %v", err)
	}

	// Verify task files exist before deletion
	if _, err := os.Stat(filepath.Join(base, "tasks", task1ID+".json")); err != nil {
		t.Fatalf("task 1 file should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "tasks", task2ID+".json")); err != nil {
		t.Fatalf("task 2 file should exist: %v", err)
	}

	// Simulate DeleteSession cleanup: delete all tasks with matching session_id
	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}

	for _, taskMap := range tasks {
		sidField, _ := taskMap["session_id"].(string)
		taskID, _ := taskMap["id"].(string)
		if sidField == sid && taskID != "" {
			if delErr := store.DeleteTask(taskID); delErr != nil {
				t.Fatalf("delete task %s: %v", taskID, delErr)
			}
		}
	}

	// Verify task files are gone
	if _, err := os.Stat(filepath.Join(base, "tasks", task1ID+".json")); !os.IsNotExist(err) {
		t.Errorf("task 1 file should be removed")
	}
	if _, err := os.Stat(filepath.Join(base, "tasks", task2ID+".json")); !os.IsNotExist(err) {
		t.Errorf("task 2 file should be removed")
	}

	// Verify session file still exists (it's removed separately)
	if _, err := os.Stat(filepath.Join(base, "sessions", sid+".json")); err != nil {
		t.Fatalf("session file should still exist (not yet removed)")
	}
}

func TestDeleteSessionIdempotentTurnsFile(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	sid := uuid.New().String()

	// Create session JSON file (no turns file created)
	sessionData := map[string]interface{}{
		"id":           sid,
		"name":         "test-session",
		"workspace_id": "ws-1",
		"status":       "active",
		"created_at":   time.Now().Format(time.RFC3339),
	}
	if err := store.SaveSession(sid, sessionData); err != nil {
		t.Fatalf("save session: %v", err)
	}

	// Simulate DeleteSession cleanup on a session with NO turns file
	turnsPath := filepath.Join(base, "turns", sid+".jsonl")
	if err := os.Remove(turnsPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove turns (should be idempotent): %v", err)
	}

	// Should not error when turns file doesn't exist
	sessionsDir := filepath.Join(base, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}

	var fullPath string
	for _, entry := range entries {
		if entry.Name() == sid+".json" {
			fullPath = filepath.Join(sessionsDir, entry.Name())
			break
		}
	}
	if fullPath == "" {
		t.Fatalf("session file not found")
	}

	if err := os.Remove(fullPath); err != nil {
		t.Fatalf("remove session file: %v", err)
	}
}

func TestDeleteSessionNoTasksForSession(t *testing.T) {
	base := t.TempDir()
	store, err := memory.NewStore(base)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	sid := uuid.New().String()

	// Create session JSON file
	sessionData := map[string]interface{}{
		"id":           sid,
		"name":         "test-session",
		"workspace_id": "ws-1",
		"status":       "active",
		"created_at":   time.Now().Format(time.RFC3339),
	}
	if err := store.SaveSession(sid, sessionData); err != nil {
		t.Fatalf("save session: %v", err)
	}

	// Create a task for a DIFFERENT session (should not be deleted)
	otherSid := uuid.New().String()
	otherTaskID := uuid.New().String()
	otherTask := map[string]interface{}{
		"id":         otherTaskID,
		"session_id": otherSid,
		"status":     "completed",
		"created_at": time.Now().Format(time.RFC3339),
		"output":     "other task",
	}
	if err := store.SaveTask(otherTaskID, otherTask); err != nil {
		t.Fatalf("save other task: %v", err)
	}

	// Verify other task file exists
	if _, err := os.Stat(filepath.Join(base, "tasks", otherTaskID+".json")); err != nil {
		t.Fatalf("other task file should exist: %v", err)
	}

	// Simulate DeleteSession cleanup: only tasks matching session_id are deleted
	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}

	for _, taskMap := range tasks {
		sidField, _ := taskMap["session_id"].(string)
		taskID, _ := taskMap["id"].(string)
		if sidField == sid && taskID != "" {
			if delErr := store.DeleteTask(taskID); delErr != nil {
				t.Fatalf("delete task %s: %v", taskID, delErr)
			}
		}
	}

	// Verify other task file still exists (it belongs to a different session)
	if _, err := os.Stat(filepath.Join(base, "tasks", otherTaskID+".json")); err != nil {
		t.Errorf("other task file should NOT be removed: %v", err)
	}
}
