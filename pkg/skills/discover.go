// Package skills provides skill discovery and progressive activation for Norman.
package skills

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillEntry represents a discovered skill with its metadata.
type SkillEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Tools       []string `json:"tools,omitempty"`
	Activation  string   `json:"activation,omitempty"` // "manual", "auto", "on-demand"

	// MCPConnection holds optional MCP endpoint configuration for this skill.
	MCPEndpoint    string   `json:"mcp_endpoint,omitempty"`
	MCPCapabilities []string `json:"mcp_capabilities,omitempty"`

	// Dependencies lists pip packages to install before activation (for Python-based skills).
	Dependencies []string `json:"dependencies,omitempty"`

	// SourceURL tracks the git repository URL this skill was installed from.
	SourceURL string `json:"source_url,omitempty"`

	// Version tracks the git tag/branch/commit SHA this skill is pinned to.
	Version string `json:"version,omitempty"`
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

// Install clones a git repository into the skills directory and validates SKILL.md.
// Returns the installed SkillEntry or an error if cloning or validation fails.
func Install(repo, version, destBaseDir string) (*SkillEntry, error) {
	// Determine destination directory name from repo basename
	destDir := filepath.Join(destBaseDir, filepath.Base(repo))

	// Clone the repository
	cmd := exec.Command("git", "clone", repo, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone failed: %w (output: %s)", err, string(out))
	}

	// Checkout specific version/tag/branch if provided
	if version != "" {
		cmd = exec.Command("git", "-C", destDir, "checkout", version)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git checkout failed: %w (output: %s)", err, string(out))
		}
	}

	// Validate SKILL.md exists and parse metadata
	entry, err := readSkillMetadata(destDir)
	if err != nil {
		return nil, fmt.Errorf("invalid skill definition at %s: %w", destDir, err)
	}

	return &entry, nil
}

// Update clones the git repository into the skills directory and validates SKILL.md.
// Similar to Install but designed for re-installation from the same source.
func Update(repo, version, destBaseDir string) (*SkillEntry, error) {
	// Determine destination directory name from repo basename
	destDir := filepath.Join(destBaseDir, filepath.Base(repo))

	// Clone (or re-clone) the repository
	cmd := exec.Command("git", "clone", repo, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone failed: %w (output: %s)", err, string(out))
	}

	// Checkout specific version/tag/branch if provided
	if version != "" {
		cmd = exec.Command("git", "-C", destDir, "checkout", version)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git checkout failed: %w (output: %s)", err, string(out))
		}
	}

	// Validate SKILL.md exists and parse metadata
	entry, err := readSkillMetadata(destDir)
	if err != nil {
		return nil, fmt.Errorf("invalid skill definition at %s: %w", destDir, err)
	}

	return &entry, nil
}

// Uninstall removes a skill directory from the skills base path.
func Uninstall(name string, destBaseDir string) error {
	destDir := filepath.Join(destBaseDir, name)
	if _, err := os.Stat(destDir); os.IsNotExist(err) {
		return fmt.Errorf("skill '%s' not found at %s", name, destDir)
	}

	return os.RemoveAll(destDir)
}

// InstallDependencies writes a temporary requirements.txt and runs pip install
// for the skill's declared dependencies. Non-fatal — logs a warning on failure
// but does not block skill activation. Returns any error encountered.
func InstallDependencies(entry SkillEntry) error {
	if len(entry.Dependencies) == 0 {
		return nil // nothing to install
	}

	// Write temporary requirements.txt in the skill's directory
	reqFile := filepath.Join(entry.Path, ".requirements.txt")
	reqContent := strings.Join(entry.Dependencies, "\n") + "\n"
	if err := os.WriteFile(reqFile, []byte(reqContent), 0644); err != nil {
		return fmt.Errorf("write requirements.txt: %w", err)
	}

	// Run pip install inside the skill directory
	cmd := exec.Command("pip", "install", "-r", reqFile)
	cmd.Dir = entry.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pip install failed for %s: %w (output: %s)", entry.Name, err, string(out))
	}

	return nil
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

		// MCP fields (optional)
		MCPEndpoint    string   `yaml:"mcp_endpoint"`
		MCPCapabilities []string `yaml:"mcp_capabilities"`

		// Install provenance fields (optional)
		SourceURL string `yaml:"source_url"`
		Version   string `yaml:"version"`
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

	entry := SkillEntry{
		Name:        meta.Name,
		Description: meta.Description,
		Path:        dir,
		Tools:       meta.Tools,
		Activation:  meta.Activation,
	}

	// Only set MCP fields if endpoint is provided (optional)
	if meta.MCPEndpoint != "" {
		entry.MCPEndpoint = meta.MCPEndpoint
		entry.MCPCapabilities = meta.MCPCapabilities
	}

	// Set provenance fields if present in frontmatter
	if meta.SourceURL != "" {
		entry.SourceURL = meta.SourceURL
	}
	if meta.Version != "" {
		entry.Version = meta.Version
	}

	return entry, nil
}
