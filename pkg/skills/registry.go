// Package skills provides a persistent global registry of installed skills.
// The registry tracks what's installed per workspace without holding actual files on the host.
package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RegistryEntry holds metadata about an installed skill and which workspaces it's deployed to.
type RegistryEntry struct {
	SourceURL           string   `json:"source_url"`
	Version             string   `json:"version"`
	Description         string   `json:"description"`
	InstalledInWorkspaces []string `json:"installed_in_workspaces"`
}

// Registry is a thread-safe global registry of skills.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]RegistryEntry
	filePath string
}

// NewRegistry creates a new registry backed by the given file path.
func NewRegistry(filePath string) *Registry {
	r := &Registry{
		entries:  make(map[string]RegistryEntry),
		filePath: filePath,
	}
	r.Load()
	return r
}

// Load reads the registry from disk. Returns nil if file doesn't exist (empty registry).
func (r *Registry) Load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // empty registry is valid
		}
		return fmt.Errorf("read registry file: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var entries map[string]RegistryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse registry: %w", err)
	}

	r.entries = entries
	return nil
}

// Save writes the registry to disk.
func (r *Registry) Save() error {
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}

	dir := filepath.Dir(r.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}

	return os.WriteFile(r.filePath, data, 0644)
}

// Register adds or updates a skill entry in the registry.
func (r *Registry) Register(name string, entry RegistryEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.entries[name]; ok {
		// Merge: keep workspaces from both old and new
		workspaceSet := make(map[string]bool)
		for _, ws := range existing.InstalledInWorkspaces {
			workspaceSet[ws] = true
		}
		for _, ws := range entry.InstalledInWorkspaces {
			workspaceSet[ws] = true
		}

		entry.InstalledInWorkspaces = make([]string, 0, len(workspaceSet))
		for ws := range workspaceSet {
			entry.InstalledInWorkspaces = append(entry.InstalledInWorkspaces, ws)
		}
	} else {
		r.entries[name] = entry
	}

	return r.Save()
}

// Unregister removes a skill from the registry.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.entries, name)
	return r.Save()
}

// RemoveWorkspace removes all references to a workspace from all entries.
func (r *Registry) RemoveWorkspace(ws string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, entry := range r.entries {
		newWorkspaces := make([]string, 0, len(entry.InstalledInWorkspaces))
		for _, w := range entry.InstalledInWorkspaces {
			if w != ws {
				newWorkspaces = append(newWorkspaces, w)
			}
		}
		entry.InstalledInWorkspaces = newWorkspaces

		// If no workspaces left, consider removing the entry entirely
		if len(newWorkspaces) == 0 {
			delete(r.entries, name)
		} else {
			r.entries[name] = entry
		}
	}

	return r.Save()
}

// GetEntry returns a skill entry by name, or nil if not found.
func (r *Registry) GetEntry(name string) (*RegistryEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[name]
	if !ok {
		return nil, false
	}
	return &entry, true
}

// GetAll returns a copy of all registry entries.
func (r *Registry) GetAll() map[string]RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]RegistryEntry, len(r.entries))
	for k, v := range r.entries {
		result[k] = v
	}
	return result
}

// AddWorkspace adds a workspace name to a skill's installed list.
func (r *Registry) AddWorkspace(name string, ws string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("skill '%s' not registered", name)
	}

	for _, w := range entry.InstalledInWorkspaces {
		if w == ws {
			return nil // already present
		}
	}

	entry.InstalledInWorkspaces = append(entry.InstalledInWorkspaces, ws)
	r.entries[name] = entry
	return r.Save()
}

// RemoveWorkspaceFromSkill removes a workspace from a specific skill's list.
func (r *Registry) RemoveWorkspaceFromSkill(name string, ws string) error {
	r.mu.Lock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("skill '%s' not registered", name)
	}

	newWorkspaces := make([]string, 0, len(entry.InstalledInWorkspaces))
	for _, w := range entry.InstalledInWorkspaces {
		if w != ws {
			newWorkspaces = append(newWorkspaces, w)
		}
	}
	entry.InstalledInWorkspaces = newWorkspaces

	if len(newWorkspaces) == 0 {
		delete(r.entries, name)
	} else {
		r.entries[name] = entry
	}

	return r.Save()
}
