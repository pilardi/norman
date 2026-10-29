// Package wiki provides the core memory engine: CRUD operations, decay logic, and staleness tracking.
package wiki

import (
	"fmt"
	"os"
	"os/exec"
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

	slug := TitleToSlug(entry.Title, entry.Type)
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

// CreateWithWorkspace creates a new wiki entry and sets the workspace field.
// This is a convenience wrapper around Create that ensures the entry is tagged
// with the current workspace for LTM scoping.
func (e *Engine) CreateWithWorkspace(workspace string, entry *WikiEntry) error {
	entry.Workspace = workspace
	return e.Create(entry)
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
		_ = os.Remove(tmp)
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

// WriteSchema writes a schema file to the wiki base directory.
// The slug is used as the filename (without .md extension).
// Content is stored as plain markdown body; frontmatter is generated automatically.
func (e *Engine) WriteSchema(slug string, content string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := filepath.Join(e.base, slug+".md")

	// Generate minimal frontmatter — title is derived from the slug
	title := strings.ReplaceAll(slug, "-", " ")
	frontmatter := fmt.Sprintf("---\ntitle: %s\ntype: schema\nslug: %s\nupdated: %s\n---\n\n", title, slug, time.Now().UTC().Format(time.RFC3339))

	data := []byte(frontmatter + content)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write schema: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename schema: %w", err)
	}

	return nil
}

// ReadSchema reads a schema file and returns its content (markdown body after frontmatter).
// Returns ErrNotFound if the slug doesn't exist.
func (e *Engine) ReadSchema(slug string) (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	path := filepath.Join(e.base, slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("schema not found: %s", slug)
		}
		return "", fmt.Errorf("read schema: %w", err)
	}

	content, err := ParseSchemaBody(string(data))
	if err != nil {
		return "", fmt.Errorf("parse schema body: %w", err)
	}
	return content, nil
}

// GitDiff returns the git diff of a specific schema file (staged changes).
// Returns the diff output as bytes; returns nil if no staged changes.
func (e *Engine) GitDiff(slug string) ([]byte, error) {
	cmd := exec.Command("git", "-C", e.base, "diff", "--cached", slug+".md")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// git diff returns exit code 1 if there are changes, 0 if clean
		if strings.Contains(string(out), "no changes") || (len(out) == 0 && err == nil) {
			return nil, nil // no staged changes
		}
		return out, fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// GitCommit runs git add and git commit with the given message.
// Returns nil on success; errors are returned without failing silently.
func (e *Engine) GitCommit(message string) error {
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = e.base
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	cmd = exec.Command("git", "commit", "--no-verify", "-m", message, "--allow-empty-message")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	cmd.Dir = e.base
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	return nil
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
			slug := TitleToSlug(entry.Title, entry.Type)
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
	Types     []EntryType // if empty, all types match
	Tags      []string    // if non-empty, entry must have ALL tags
	Workspace string      // if non-empty, entry must match this workspace
	Stale     bool        // if true, only return stale entries; if false, only non-stale
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

	if f.Workspace != "" {
		if entry.Workspace != f.Workspace {
			return false
		}
	}

	if f.Stale {
		return entry.IsStale()
	}
	return !entry.IsStale()
}
