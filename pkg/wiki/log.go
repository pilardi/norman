// Package wiki provides an append-only journal for wiki events.
package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EventType represents a journal entry type.
type EventType string

const (
	EventIngest EventType = "INGEST"
	EventUpdate EventType = "UPDATE"
	EventQuery  EventType = "QUERY"
	EventPrune  EventType = "PRUNE"
)

// Journal manages an append-only event log file.
type Journal struct {
	mu   sync.Mutex
	path string // e.g., ~/.norman/memory/log.md
}

// NewJournal creates a new journal writing to the given path.
func NewJournal(path string) (*Journal, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return &Journal{path: path}, nil
}

// WriteAppends a formatted journal entry. Entries are appended (never overwritten).
func (j *Journal) Write(event EventType, description string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	ts := time.Now().UTC().Format("2006-01-02 15:04")
	line := fmt.Sprintf("[%s] %s: %s\n", ts, event, description)

	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	_, err = f.WriteString(line)
	if err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

// ReadAll returns the full journal content.
func (j *Journal) ReadAll() ([]string, error) {
	data, err := os.ReadFile(j.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read log: %w", err)
	}
	if len(data) == 0 {
		return []string{}, nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return lines, nil
}

// TruncateLast removes the last N entries from the journal.
func (j *Journal) TruncateLast(n int) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read log: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if n >= len(lines) {
		return os.WriteFile(j.path, []byte(""), 0644)
	}
	lines = lines[:len(lines)-n]
	return os.WriteFile(j.path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
