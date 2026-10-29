// Package memory provides a read-only raw sources store for Norman.
//
// RawSources captures snapshots of environment data (env vars, config reads,
// API responses, filesystem state) as structured JSON objects. This is distinct
// from wiki entries which are structured markdown with frontmatter types.
//
// Usage: call Snapshot() to record a read; Query() to retrieve stored sources.
// Designed for context injection into the agent loop — not for persistence
// of task/session state (that belongs in Store).

package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Source represents a single raw environment read snapshot.
type Source struct {
	ID        string        `json:"id"`            // unique identifier (e.g., "env:HOME", "config:norman")
	Name      string        `json:"name"`          // human-readable name
	Category  string        `json:"category"`      // e.g., "env", "config", "api", "filesystem"
	Content   string        `json:"content"`       // the raw data as a string (JSON-serialized if structured)
	CreatedAt time.Time     `json:"created_at"`    // when this snapshot was taken
	TTL       time.Duration `json:"ttl,omitempty"` // optional TTL; zero = no expiry
}

// RawSources is a read-only snapshot store for environment reads.
// It provides context injection into the agent loop without modifying wiki entries.
type RawSources struct {
	mu   sync.RWMutex
	base string // directory path for raw source snapshots
}

// NewRawSources creates a new raw sources store backed by the given base directory.
func NewRawSources(baseDir string) (*RawSources, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create raw sources dir: %w", err)
	}
	return &RawSources{base: baseDir}, nil
}

// Snapshot records a new environment read as a raw source entry.
// Returns the saved Source with auto-generated fields (ID, Category).
func (r *RawSources) Snapshot(source Source) (*Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Generate ID if not provided (use nanosecond precision to avoid collisions).
	if source.ID == "" {
		source.ID = fmt.Sprintf("src-%s", time.Now().Format("20060102-150405.000000000"))
	}

	// Ensure category is set
	if source.Category == "" {
		source.Category = "general"
	}

	// Ensure name is set
	if source.Name == "" {
		source.Name = source.ID
	}

	// Store as JSON file (atomic write)
	dir := filepath.Join(r.base, "sources")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create sources dir: %w", err)
	}

	path := filepath.Join(dir, source.ID+".json")

	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal source: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return nil, fmt.Errorf("write source snapshot: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("rename source snapshot: %w", err)
	}

	return &source, nil
}

// Query retrieves a source by ID. Returns an error if not found.
func (r *RawSources) Query(id string) (*Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	path := filepath.Join(r.base, "sources", id+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", id, err)
	}

	var source Source
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("parse source %s: %w", id, err)
	}

	return &source, nil
}

// ListAll returns all stored sources, sorted by CreatedAt descending.
func (r *RawSources) ListAll() ([]Source, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dir := filepath.Join(r.base, "sources")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read sources dir: %w", err)
	}

	var sources []Source
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}

		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}

		var source Source
		if err := json.Unmarshal(data, &source); err != nil {
			continue // skip malformed JSON
		}

		sources = append(sources, source)
	}

	// Sort by CreatedAt descending (most recent first)
	for i := 0; i < len(sources)-1; i++ {
		for j := i + 1; j < len(sources); j++ {
			if sources[j].CreatedAt.After(sources[i].CreatedAt) {
				sources[i], sources[j] = sources[j], sources[i]
			}
		}
	}

	return sources, nil
}

// QueryByCategory returns all sources matching a given category.
func (r *RawSources) QueryByCategory(category string) ([]Source, error) {
	all, err := r.ListAll()
	if err != nil {
		return nil, err
	}

	var result []Source
	for _, s := range all {
		if s.Category == category {
			result = append(result, s)
		}
	}

	return result, nil
}

// Count returns the number of stored sources.
func (r *RawSources) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dir := filepath.Join(r.base, "sources")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			count++
		}
	}

	return count
}

// IsExpired checks if a source has passed its TTL.
func (s *Source) IsExpired() bool {
	if s.TTL == 0 {
		return false // no expiry configured
	}
	return time.Since(s.CreatedAt) > s.TTL
}
