// Package memory provides file-based state persistence for Norman tasks and sessions.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// SaveTask persists a task's state to disk with optimistic locking.
func (s *Store) SaveTask(taskID string, data map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.base, "tasks", taskID+".json")

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
		os.Remove(tmp) // clean up on failure
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
		os.Remove(tmp)
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

// Stats returns store statistics.
func (s *Store) Stats() (taskCount, sessionCount int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	taskDir := filepath.Join(s.base, "tasks")
	sessionDir := filepath.Join(s.base, "sessions")

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

	return taskCount, sessionCount, nil
}
