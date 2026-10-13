// Package skills provides skill discovery and progressive activation for Norman.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillEntry represents a discovered skill with its metadata.
type SkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Tools       []string `json:"tools,omitempty"`
	Activation  string `json:"activation,omitempty"` // "manual", "auto", "on-demand"
}

// DiscoverResult holds the result of a discovery scan.
type DiscoverResult struct {
	Skills    []SkillEntry
	Scanned   int // total directories scanned
	Valid     int // directories with valid SKILL.md
}

// Discover scans configured paths for skill definitions and returns metadata only.
func Discover(paths []string) (*DiscoverResult, error) {
	result := &DiscoverResult{}

	for _, baseDir := range paths {
		// Expand ~ to user home directory
		baseDir = filepath.Clean(os.ExpandEnv(baseDir))
		if strings.HasPrefix(baseDir, "~/") {
			home, err := os.UserHomeDir()
			if err == nil {
				baseDir = filepath.Join(home, baseDir[2:])
			}
		}

		entries, err := os.ReadDir(baseDir)
		if err != nil {
			// Skip missing directories (not an error — some discovery paths may not exist yet)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(baseDir, entry.Name())
			metadata, err := readSkillMetadata(skillPath)
			if err != nil {
				result.Scanned++
				continue // invalid skill definition, skip silently
			}

			metadata.Path = skillPath
			result.Skills = append(result.Skills, metadata)
			result.Valid++
		}
		result.Scanned++
	}

	return result, nil
}

// readSkillMetadata reads and parses a SKILL.md file, returning its metadata.
func readSkillMetadata(dir string) (SkillEntry, error) {
	skillFile := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return SkillEntry{}, fmt.Errorf("read SKILL.md: %w", err)
	}

	content := string(data)

	// Extract frontmatter (between --- delimiters)
	frontmatterStart := strings.Index(content, "---")
	if frontmatterStart == -1 {
		return SkillEntry{}, fmt.Errorf("no frontmatter found")
	}

	frontmatterEnd := strings.Index(content[frontmatterStart+3:], "---")
	if frontmatterEnd == -1 {
		return SkillEntry{}, fmt.Errorf("unclosed frontmatter")
	}

	frontmatterEnd += 3 // account for the leading ---
	fmContent := content[frontmatterStart+3:frontmatterEnd]

	var meta struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Tools       []string `yaml:"tools"`
		Activation  string   `yaml:"activation"`
	}

	if err := yaml.Unmarshal([]byte(fmContent), &meta); err != nil {
		return SkillEntry{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	if meta.Name == "" {
		return SkillEntry{}, fmt.Errorf("missing 'name' in frontmatter")
	}

	meta.Activation = strings.ToLower(meta.Activation)
	if meta.Activation == "" {
		meta.Activation = "on-demand" // default
	}

	return SkillEntry{
		Name:        meta.Name,
		Description: meta.Description,
		Path:        dir,
		Tools:       meta.Tools,
		Activation:  meta.Activation,
	}, nil
}
