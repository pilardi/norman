// Package wiki provides index/catalog maintenance for the Norman memory engine.
package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IndexEntry represents a single row in the catalog table.
type IndexEntry struct {
	Page    string
	Type    EntryType
	Tags    []string
	Updated string // "YYYY-MM-DD" format
}

// BuildIndex reads all wiki entries and returns an IndexEntry slice sorted by updated time (newest first).
func BuildIndex(baseDir string) ([]IndexEntry, error) {
	files, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	var entries []IndexEntry
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".md") || strings.HasPrefix(f.Name(), "index.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(baseDir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name(), err)
		}
		entry, parseErr := ParseFrontmatter(string(data))
		if parseErr != nil {
			continue // skip unparseable files
		}
		entries = append(entries, IndexEntry{
			Page:    strings.TrimSuffix(f.Name(), ".md"),
			Type:    entry.Type,
			Tags:    entry.Tags,
			Updated: entry.Updated.Format("2006-01-02"),
		})
	}

	// Sort newest first
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Updated > entries[i].Updated {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	return entries, nil
}

// WriteIndex writes the catalog index.md file to the base directory.
func WriteIndex(baseDir string, entries []IndexEntry) error {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("title: \"Content Catalog\"\n")
	sb.WriteString("type: system\n")
	fmt.Fprintf(&sb, "updated: %s\n", time.Now().UTC().Format("2006-01-02"))
	sb.WriteString("---\n\n")
	sb.WriteString("# Content Catalog\n\n")

	if len(entries) == 0 {
		sb.WriteString("*No entries yet.*\n\n")
	} else {
		sb.WriteString("| Page | Type | Tags | Updated |\n")
		sb.WriteString("|------|------|------|---------|\n")
		for _, e := range entries {
			tags := strings.Join(e.Tags, ", ")
			if tags == "" {
				tags = "—"
			}
			fmt.Fprintf(&sb, "| %s | %s | %s | %s |\n",
				e.Page, string(e.Type), tags, e.Updated)
		}
		sb.WriteString("\n")
	}

	path := filepath.Join(baseDir, "index.md")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	return os.Rename(tmp, path)
}
