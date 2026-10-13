// Package wiki provides a structured entry model and frontmatter parser for the Norman memory engine.
package wiki

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EntryType represents the classification of a wiki entry.
type EntryType string

const (
	EntryTypeEntity   EntryType = "entity"
	EntryTypeSource   EntryType = "source"
	EntryTypeTrend    EntryType = "trend"
	EntryTypeTask     EntryType = "task"
)

// Valid checks whether the entry type is one of the recognized classifications.
func (t EntryType) Valid() bool {
	switch t {
	case EntryTypeEntity, EntryTypeSource, EntryTypeTrend, EntryTypeTask:
		return true
	default:
		return false
	}
}

// WikiEntry represents a single wiki entry with frontmatter metadata and markdown body.
type WikiEntry struct {
	Title     string    `yaml:"title"`
	Type      EntryType `yaml:"type"`
	Tags      []string  `yaml:"tags,omitempty"`
	Pinned    bool      `yaml:"pinned,omitempty"`
	Updated   time.Time `yaml:"updated"`
	CreatedAt time.Time `yaml:"created_at"`
	Content   string    // markdown body (after frontmatter)
}

// IsStale returns true if the entry is a "trend" type that hasn't been updated in >30 days.
func (e *WikiEntry) IsStale() bool {
	return e.Type == EntryTypeTrend && time.Since(e.Updated) > 30*24*time.Hour
}

// HasTag returns true if the entry has the given tag.
func (e *WikiEntry) HasTag(tag string) bool {
	for _, t := range e.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// TitleToSlug converts a title into a filesystem-safe slug.
func TitleToSlug(title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	// Remove characters that are not alphanumeric or hyphen
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	slug = b.String()
	return filepath.Clean(slug)
}

// ParseFrontmatter parses the YAML frontmatter from a markdown string.
// Returns the parsed entry and the remaining body text (after the closing `---`).
func ParseFrontmatter(markdown string) (*WikiEntry, error) {
	trimmed := strings.TrimSpace(markdown)
	if !strings.HasPrefix(trimmed, "---") {
		return nil, fmt.Errorf("no frontmatter delimiter found")
	}

	// Find closing delimiter
	endIdx := strings.Index(trimmed[3:], "\n---\n")
	if endIdx == -1 {
		// Try without trailing newline (edge case)
		endIdx = strings.Index(trimmed[3:], "\n---")
		if endIdx == -1 {
			return nil, fmt.Errorf("no closing frontmatter delimiter found")
		}
	}

	frontmatter := trimmed[3 : 3+endIdx]
	body := strings.TrimSpace(markdown[3+endIdx+4:])

	var entry WikiEntry
	if err := yaml.Unmarshal([]byte(frontmatter), &entry); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	entry.Content = body

	// Validate required fields
	if entry.Title == "" {
		return nil, fmt.Errorf("frontmatter must contain 'title'")
	}
	if !entry.Type.Valid() {
		return nil, fmt.Errorf("invalid entry type: %q", entry.Type)
	}

	// Set timestamps if not provided
	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.Updated.IsZero() {
		entry.Updated = now
	}

	return &entry, nil
}

// ToMarkdown serializes a WikiEntry back to markdown with frontmatter.
func (e *WikiEntry) ToMarkdown() string {
	yamlBytes, _ := yaml.Marshal(map[string]interface{}{
		"title":     e.Title,
		"type":      string(e.Type),
		"tags":      e.Tags,
		"pinned":    e.Pinned,
		"updated":   e.Updated.Format("2006-01-02"),
		"created_at": e.CreatedAt.Format("2006-01-02"),
	})

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---\n\n")
	if e.Content != "" {
		sb.WriteString(e.Content)
	}
	return sb.String()
}
