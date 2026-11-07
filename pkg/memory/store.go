// Package memory provides file-based state persistence for Norman tasks and sessions.
package memory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Store manages persistent task and session state on disk.
type Store struct {
	mu   sync.RWMutex
	base string // directory path for all stored data
}

// NewStore creates a new memory store backed by the given base directory.
func NewStore(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return &Store{base: baseDir}, nil
}

// Base returns the base directory path for this store.
func (s *Store) Base() string {
	return s.base
}

// SaveTask persists a task's state to disk with optimistic locking.
func (s *Store) SaveTask(taskID string, data map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "tasks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create tasks dir: %w", err)
	}

	path := filepath.Join(s.base, "tasks", taskID+".json")

	return s.saveFile(path, data)
}

// ArchiveSession writes a full transcript to the sessions directory as JSON.
func (s *Store) ArchiveSession(taskID string, turns []interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	path := filepath.Join(dir, taskID+".json")

	// Collect turns into a single JSON array with metadata
	var entries []map[string]interface{}
	for _, turn := range turns {
		data, ok := turn.(map[string]interface{})
		if !ok {
			continue
		}
		data["archived_at"] = time.Now().Format(time.RFC3339)
		entries = append(entries, data)
	}

	content, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session archive: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return fmt.Errorf("write session archive: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session archive: %w", err)
	}
	return nil
}

// saveFile writes data to a JSON file atomically (caller must hold s.mu).
func (s *Store) saveFile(path string, data map[string]interface{}) error {
	// Write atomically via temp file + rename
	tmp := path + ".tmp"
	data["updated_at"] = time.Now().Format(time.RFC3339)

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task data: %w", err)
	}

	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // clean up on failure
		return fmt.Errorf("rename task file: %w", err)
	}

	return nil
}

// LoadTask reads a task's state from disk.
func (s *Store) LoadTask(taskID string) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "tasks", taskID+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("task %s not found", taskID)
		}
		return nil, fmt.Errorf("read task file: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse task data: %w", err)
	}

	return result, nil
}

// ListTasks returns all stored tasks.
func (s *Store) ListTasks() ([]map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]map[string]interface{}, 0)

	dir := filepath.Join(s.base, "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return tasks, nil // no tasks yet
		}
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // skip unreadable files
		}

		var task map[string]interface{}
		if err := json.Unmarshal(data, &task); err != nil {
			continue // skip corrupt files
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}

// DeleteTask removes a task's persisted state.
func (s *Store) DeleteTask(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.base, "tasks", taskID+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return fmt.Errorf("delete task file: %w", err)
	}

	return nil
}

// SaveSession persists a session's state.
func (s *Store) SaveSession(sessionID string, data map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	path := filepath.Join(s.base, "sessions", sessionID+".json")

	data["updated_at"] = time.Now().Format(time.RFC3339)

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session data: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session file: %w", err)
	}

	return nil
}

// LoadSession reads a session's state from disk.
func (s *Store) LoadSession(sessionID string) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "sessions", sessionID+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %s not found", sessionID)
		}
		return nil, fmt.Errorf("read session file: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse session data: %w", err)
	}

	return result, nil
}

// DeleteSession removes a session's state from disk.
func (s *Store) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.base, "sessions", sessionID+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %s not found", sessionID)
		}
		return fmt.Errorf("delete session file: %w", err)
	}

	return nil
}

// ListSessions returns all active sessions.
func (s *Store) ListSessions() []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Join(s.base, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var sessions []map[string]interface{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			log.Printf("memory: failed to read session file %s: %v", entry.Name(), err)
			continue
		}

		var session map[string]interface{}
		if err := json.Unmarshal(data, &session); err != nil {
			log.Printf("memory: failed to parse session file %s: %v", entry.Name(), err)
			continue
		}

		sessions = append(sessions, session)
	}

	return sessions
}

// SaveTranscript writes a conversation transcript as markdown to the sessions directory.
// Each entry is formatted as "## Turn N: [role]\n\ncontent\n" with timestamps.
func (s *Store) SaveTranscript(sessionID string, entries []map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	var lines []string
	lines = append(lines, "# Session Transcript\n")
	lines = append(lines, fmt.Sprintf("**Session ID:** `%s`\n\n", sessionID))
	lines = append(lines, fmt.Sprintf("**Created:** %s\n\n---\n\n", time.Now().Format(time.RFC3339)))

	for i, entry := range entries {
		role, _ := entry["role"].(string)
		content, _ := entry["content"].(string)
		timestamp, _ := entry["timestamp"].(string)

		lines = append(lines, fmt.Sprintf("## Turn %d: %s", i+1, role))
		if timestamp != "" {
			lines = append(lines, fmt.Sprintf("\n\n*%s*\n", timestamp))
		} else {
			lines = append(lines, "\n")
		}
		lines = append(lines, content+"\n")
		lines = append(lines, "\n---\n\n")
	}

	path := filepath.Join(dir, sessionID+".md")
	tmp := path + ".tmp"
	content := strings.Join(lines, "")
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename transcript: %w", err)
	}

	return nil
}

// LoadTranscript reads a session's markdown transcript from disk.
func (s *Store) LoadTranscript(sessionID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "sessions", sessionID+".md")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("transcript for session %s not found", sessionID)
		}
		return "", fmt.Errorf("read transcript: %w", err)
	}

	return string(data), nil
}

// Prune removes completed tasks older than the given age.
func (s *Store) Prune(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	pruned := 0
	cutoff := time.Now().Add(-maxAge)

	dir := filepath.Join(s.base, "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var task map[string]interface{}
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}

		completedAt, ok := task["completed_at"].(string)
		if !ok {
			continue // still running or no completion time
		}

		ct, err := time.Parse(time.RFC3339, completedAt)
		if err != nil {
			continue
		}

		if ct.Before(cutoff) {
			path := filepath.Join(dir, entry.Name())
			if err := os.Remove(path); err == nil {
				pruned++
			}
		}
	}

	return pruned
}

// SavePlan persists a plan's content to disk as JSON.
func (s *Store) SavePlan(planID string, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "plans")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create plans dir: %w", err)
	}

	path := filepath.Join(dir, planID+".json")

	data := map[string]interface{}{
		"id":         planID,
		"content":    content,
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	}

	contentBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan data: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, contentBytes, 0644); err != nil {
		return fmt.Errorf("write plan file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename plan file: %w", err)
	}

	return nil
}

// LoadPlan reads a plan's content from disk.
func (s *Store) LoadPlan(planID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "plans", planID+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("plan %s not found", planID)
		}
		return "", fmt.Errorf("read plan file: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse plan data: %w", err)
	}

	content, ok := result["content"].(string)
	if !ok {
		return "", fmt.Errorf("plan content not a string")
	}

	return content, nil
}

// DeletePlan removes a plan's persisted state.
func (s *Store) DeletePlan(planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.base, "plans", planID+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return fmt.Errorf("delete plan file: %w", err)
	}

	return nil
}

// ListPlans returns all stored plans.
func (s *Store) ListPlans() ([]map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	plans := make([]map[string]interface{}, 0)

	dir := filepath.Join(s.base, "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return plans, nil // no plans yet
		}
		return nil, fmt.Errorf("read plans dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // skip unreadable files
		}

		var plan map[string]interface{}
		if err := json.Unmarshal(data, &plan); err != nil {
			continue // skip corrupt files
		}

		plans = append(plans, plan)
	}

	return plans, nil
}

// SaveWorkspace persists a workspace to disk as JSON.
func (s *Store) SaveWorkspace(name string, ws interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "workspaces")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create workspaces dir: %w", err)
	}

	path := filepath.Join(dir, name+".json")

	data, ok := ws.(map[string]interface{})
	if !ok {
		return fmt.Errorf("workspace must be a map[string]interface{}")
	}

	data["updated_at"] = time.Now().Format(time.RFC3339)

	contentBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace data: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, contentBytes, 0644); err != nil {
		return fmt.Errorf("write workspace file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename workspace file: %w", err)
	}

	return nil
}

// LoadWorkspace reads a workspace from disk.
func (s *Store) LoadWorkspace(name string) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "workspaces", name+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workspace %s not found", name)
		}
		return nil, fmt.Errorf("read workspace file: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse workspace data: %w", err)
	}

	return result, nil
}

// DeleteWorkspace removes a workspace's persisted state.
func (s *Store) DeleteWorkspace(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.base, "workspaces", name+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return fmt.Errorf("delete workspace file: %w", err)
	}

	return nil
}

// ListWorkspaces returns all stored workspaces.
func (s *Store) ListWorkspaces() ([]map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspaces := make([]map[string]interface{}, 0)

	dir := filepath.Join(s.base, "workspaces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return workspaces, nil // no workspaces yet
		}
		return nil, fmt.Errorf("read workspaces dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // skip unreadable files
		}

		var ws map[string]interface{}
		if err := json.Unmarshal(data, &ws); err != nil {
			continue // skip corrupt files
		}

		workspaces = append(workspaces, ws)
	}

	return workspaces, nil
}

// PersistTurn appends a single conversation turn to the task's turns file.
// This enables incremental persistence so a crashed process can resume from the last turn.
func (s *Store) PersistTurn(taskID string, turn map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "turns")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create turns dir: %w", err)
	}

	path := filepath.Join(dir, taskID+".jsonl")

	// Append JSON line (newline-delimited JSON for incremental writes)
	entry := map[string]interface{}{
		"role":      turn["role"],
		"content":   turn["content"],
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if id, ok := turn["id"].(string); ok {
		entry["id"] = id
	}
	if name, ok := turn["name"].(string); ok {
		entry["name"] = name
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal turn entry: %w", err)
	}

	// Append directly to the path file (O_APPEND ensures correct positioning)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open turns file: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write turn entry: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close turns file: %w", err)
	}

	return nil
}

// LoadTurns reads all persisted turns for a task from its JSONL file.
func (s *Store) LoadTurns(taskID string) ([]map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "turns", taskID+".jsonl")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no turns persisted yet
		}
		return nil, fmt.Errorf("read turns file: %w", err)
	}

	var entries []map[string]interface{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse turn entry: %w", err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// DeleteTurns removes a task's persisted turns file.
func (s *Store) DeleteTurns(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.base, "turns", taskID+".jsonl")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return fmt.Errorf("delete turns file: %w", err)
	}

	return nil
}

// PersistToolCallResult appends a tool call result (name, arguments, output) to the task's results file.
// This preserves raw tool call data for replay and debugging alongside text responses.
func (s *Store) PersistToolCallResult(taskID string, name string, args string, output string, exitCode int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "tool_results")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create tool_results dir: %w", err)
	}

	path := filepath.Join(dir, taskID+".jsonl")

	entry := map[string]interface{}{
		"type":      "tool_call_result",
		"name":      name,
		"arguments": args,
		"output":    output,
		"exit_code": exitCode,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal tool result entry: %w", err)
	}

	// Append directly to the path file (O_APPEND ensures correct positioning)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open tool results file: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write tool result entry: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close tool results file: %w", err)
	}

	return nil
}

// LoadToolCallResults reads all persisted tool call results for a task.
func (s *Store) LoadToolCallResults(taskID string) ([]map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "tool_results", taskID+".jsonl")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no results persisted yet
		}
		return nil, fmt.Errorf("read tool results file: %w", err)
	}

	var entries []map[string]interface{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse tool result entry: %w", err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Stats returns store statistics.
func (s *Store) Stats() (taskCount, sessionCount int, planCount int, workspaceCount int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	taskDir := filepath.Join(s.base, "tasks")
	sessionDir := filepath.Join(s.base, "sessions")
	planDir := filepath.Join(s.base, "plans")
	workspaceDir := filepath.Join(s.base, "workspaces")

	entries, err := os.ReadDir(taskDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
				taskCount++
			}
		}
	}

	entries, err = os.ReadDir(sessionDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
				sessionCount++
			}
		}
	}

	entries, err = os.ReadDir(planDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
				planCount++
			}
		}
	}

	entries, err = os.ReadDir(workspaceDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
				workspaceCount++
			}
		}
	}

	return taskCount, sessionCount, planCount, workspaceCount, nil
}

// GetSession returns a session by ID. Returns error if not found.
func (s *Store) GetSession(sessionID string) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session %s not found: %w", sessionID, err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid session data for %s: %w", sessionID, err)
	}

	return result, nil
}

// SaveScheduledTasks persists a list of scheduled task entries as JSON.
func (s *Store) SaveScheduledTasks(entries []interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.base, "tasks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create tasks dir: %w", err)
	}

	path := filepath.Join(dir, "scheduled_tasks.json")
	content, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal scheduled tasks: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return fmt.Errorf("write scheduled tasks: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename scheduled tasks: %w", err)
	}

	return nil
}

// LoadScheduledTasks loads scheduled task entries from disk.
func (s *Store) LoadScheduledTasks() ([]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.base, "tasks", "scheduled_tasks.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no scheduled tasks yet
		}
		return nil, fmt.Errorf("read scheduled tasks: %w", err)
	}

	var result []interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse scheduled tasks: %w", err)
	}

	return result, nil
}
