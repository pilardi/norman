// Package wiki provides the core memory engine: CRUD operations, decay logic, and staleness tracking.
package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Engine manages wiki entries stored as markdown files in a base directory.
type Engine struct {
	mu   sync.RWMutex
	base string // e.g., ~/.norman/memory/
}

// NewEngine creates a new wiki engine backed by the given base directory.
func NewEngine(baseDir string) (*Engine, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create wiki dir: %w", err)
	}
	return &Engine{base: baseDir}, nil
}

// Create writes a new wiki entry to disk. Returns an error if an entry with the same title exists.
func (e *Engine) Create(entry *WikiEntry) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	slug := TitleToSlug(entry.Title)
	path := filepath.Join(e.base, slug+".md")

	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("entry already exists: %s", entry.Title)
	}

	data := entry.ToMarkdown()
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	return nil
}

// Update overwrites an existing entry. Returns ErrNotFound if the slug doesn't exist.
func (e *Engine) Update(slug string, updates map[string]interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := filepath.Join(e.base, slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read entry: %w", err)
	}

	entry, err := ParseFrontmatter(string(data))
	if err != nil {
		return fmt.Errorf("parse existing entry: %w", err)
	}

	// Apply updates (support both "content" and "Content" keys)
	if v, ok := updates["title"]; ok {
		entry.Title = v.(string)
	}
	if v, ok := updates["type"]; ok {
		entry.Type = EntryType(v.(string))
	}
	if v, ok := updates["tags"]; ok {
		entry.Tags = v.([]string)
	}
	if v, ok := updates["pinned"]; ok {
		entry.Pinned = v.(bool)
	}
	if v, ok := updates["content"]; ok {
		entry.Content = v.(string)
	}

	// Always refresh updated timestamp
	entry.Updated = time.Now().UTC()

	// Write back (atomic via rename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(entry.ToMarkdown()), 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename entry: %w", err)
	}

	return nil
}

// Delete removes a wiki entry by slug and returns ErrNotFound if absent.
func (e *Engine) Delete(slug string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := filepath.Join(e.base, slug+".md")
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("entry not found: %s", slug)
		}
		return fmt.Errorf("stat entry: %w", err)
	}
	return os.Remove(path)
}

// Get loads a wiki entry by slug. Returns ErrNotFound if absent.
func (e *Engine) Get(slug string) (*WikiEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	path := filepath.Join(e.base, slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read entry: %w", err)
	}
	return ParseFrontmatter(string(data))
}

// List returns all entries matching the given filters.
// If filter is nil, all entries are returned.
func (e *Engine) List(filter *Filter) ([]*WikiEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entries, err := e.readAllEntries()
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return entries, nil
	}

	var result []*WikiEntry
	for _, entry := range entries {
		if !filter.Matches(entry) {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

// PruneStale removes entries that have exceeded their decay thresholds:
// - type: trend without update >30 days → deleted
// - type: task without tags and age >60 days → deleted
func (e *Engine) PruneStale() (removed int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	entries, err := e.readAllEntries()
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		shouldRemove := false

		switch entry.Type {
		case EntryTypeTrend:
			if time.Since(entry.Updated) > 30*24*time.Hour {
				shouldRemove = true
			}
		case EntryTypeTask:
			if len(entry.Tags) == 0 && time.Since(entry.CreatedAt) > 60*24*time.Hour {
				shouldRemove = true
			}
		}

		if shouldRemove {
			slug := TitleToSlug(entry.Title)
			path := filepath.Join(e.base, slug+".md")
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}

	return removed
}

// ListStale returns entries that are currently flagged as stale (trend >30 days).
func (e *Engine) ListStale() ([]*WikiEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entries, err := e.readAllEntries()
	if err != nil {
		return nil, err
	}

	var stale []*WikiEntry
	for _, entry := range entries {
		if entry.IsStale() {
			stale = append(stale, entry)
		}
	}
	return stale, nil
}

// Stats returns counts of entries by type.
func (e *Engine) Stats() map[EntryType]int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entries, err := e.readAllEntries()
	if err != nil {
		return nil
	}

	counts := make(map[EntryType]int)
	for _, entry := range entries {
		counts[entry.Type]++
	}
	return counts
}

// readAllEntries reads all .md files in the base directory and parses them.
func (e *Engine) readAllEntries() ([]*WikiEntry, error) {
	files, err := os.ReadDir(e.base)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	var entries []*WikiEntry
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".md") || strings.HasPrefix(f.Name(), "index.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.base, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name(), err)
		}
		entry, err := ParseFrontmatter(string(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f.Name(), err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Filter applies to List() calls for filtering results.
type Filter struct {
	Types []EntryType // if empty, all types match
	Tags  []string    // if non-empty, entry must have ALL tags
	Stale bool        // if true, only return stale entries; if false, only non-stale
}

// Matches returns true if the entry satisfies this filter.
func (f *Filter) Matches(entry *WikiEntry) bool {
	if len(f.Types) > 0 {
		found := false
		for _, t := range f.Types {
			if entry.Type == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(f.Tags) > 0 {
		for _, tag := range f.Tags {
			if !entry.HasTag(tag) {
				return false
			}
		}
	}

	if f.Stale {
		return entry.IsStale()
	}
	return !entry.IsStale()
}
