// Package skills provides skill discovery and progressive activation for Norman.
package skills

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/norman/norman/pkg/env"
	"gopkg.in/yaml.v3"
)

// SkillEntry represents a discovered skill with its metadata.
// ToolDef defines a single tool with its name, description, and typed parameter schema.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type SkillEntry struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Path        string    `json:"path"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Activation  string    `json:"activation,omitempty"` // "manual", "auto", "on-demand"

	// MCPConnection holds optional MCP endpoint configuration for this skill.
	MCPEndpoint     string   `json:"mcp_endpoint,omitempty"`
	MCPCapabilities []string `json:"mcp_capabilities,omitempty"`

	// Dependencies lists pip packages to install before activation (for Python-based skills).
	Dependencies []string `json:"dependencies,omitempty"`

	// SourceURL tracks the git repository URL this skill was installed from.
	SourceURL string `json:"source_url,omitempty"`

	// Version tracks the git tag/branch/commit SHA this skill is pinned to.
	Version string `json:"version,omitempty"`

	// BinaryPath is the host-side path to a pre-built binary for this skill.
	// If set, DeployToSandbox copies the file instead of cloning a git repo.
	BinaryPath string `json:"binary_path,omitempty"`
}

// DiscoverResult holds the result of a discovery scan.
type DiscoverResult struct {
	Skills  []SkillEntry
	Scanned int // total directories scanned
	Valid   int // directories with valid SKILL.md
}

// Discover scans configured paths for skill definitions and returns metadata only.
// Supports "builtin://" prefix for built-in skills bundled with the binary (e.g., builtin://task-management).
func Discover(paths []string) (*DiscoverResult, error) {
	result := &DiscoverResult{}

	for _, baseDir := range paths {
		// Handle builtin:// prefix — load from embedded SKILL.md files in pkg/skills/builtin/
		if strings.HasPrefix(baseDir, "builtin://") {
			skillName := strings.TrimPrefix(baseDir, "builtin://")
			if skillName == "" {
				continue // skip empty builtin entries
			}

			skillPath := filepath.Join("pkg", "skills", "builtin", skillName)
			metadata, err := readSkillMetadata(skillPath)
			if err != nil {
				log.Printf("[skills] builtin skill '%s': %v (skipping)", skillName, err)
				result.Scanned++
				continue
			}

			metadata.Path = skillPath
			result.Skills = append(result.Skills, metadata)
			result.Valid++
			result.Scanned++
			continue
		}

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

// InstallDependencies installs the skill's declared Python dependencies.
// If session is non-nil, deps are installed inside the OpenShell sandbox
// (preferred for P3 — proper isolation). Otherwise falls back to local venv.
// Non-fatal — logs a warning on failure but does not block skill activation.
func InstallDependencies(entry SkillEntry, session *env.Session) error {
	if len(entry.Dependencies) == 0 {
		return nil // nothing to install
	}

	skillDir := entry.Path

	// Write .requirements.txt in the skill's directory
	reqFile := filepath.Join(skillDir, ".requirements.txt")
	reqContent := strings.Join(entry.Dependencies, "\n") + "\n"
	if err := os.WriteFile(reqFile, []byte(reqContent), 0644); err != nil {
		return fmt.Errorf("write requirements.txt: %w", err)
	}

	// P3: If a workspace session is provided, install inside that sandbox
	if session != nil {
		ctx := context.Background()
		cmdStr := "pip install -r .requirements.txt"
		output, err := session.Exec(ctx, []string{"bash", "-c", cmdStr})
		if err != nil {
			return fmt.Errorf("pip install in sandbox '%s': %w (output: %s)", session.SessionID(), err, output)
		}
		log.Printf("[skills] installed dependencies for '%s' in sandbox '%s'", entry.Name, session.SessionID())
		return nil
	}

	// Fallback: local venv approach
	venvDir := filepath.Join(skillDir, ".venv")
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		cmd := exec.Command("python3", "-m", "venv", venvDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create venv for %s: %w (output: %s)", entry.Name, err, string(out))
		}
	}

	pipPath := filepath.Join(venvDir, "bin", "pip")
	cmd := exec.Command(pipPath, "install", "-r", reqFile)
	cmd.Dir = skillDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pip install failed for %s: %w (output: %s)", entry.Name, err, string(out))
	}

	log.Printf("[skills] installed dependencies for '%s' in %s/.venv", entry.Name, skillDir)
	return nil
}

// CheckRemoteVersion fetches the latest available version (tag or HEAD commit SHA)
// from the remote git repository. Returns the version string or an error.
func CheckRemoteVersion(repo string) (string, error) {
	// Try to get the most recent tag (sorted by version)
	tagCmd := exec.Command("git", "ls-remote", "--tags", "--sort=-v:refname", repo, "refs/tags/*")
	out, err := tagCmd.CombinedOutput()
	if err != nil {
		// Fall back to HEAD commit SHA
		headCmd := exec.Command("git", "ls-remote", "--heads", repo, "HEAD")
		headOut, headErr := headCmd.CombinedOutput()
		if headErr != nil {
			return "", fmt.Errorf("failed to fetch remote version: %w (tags: %s, head: %s)", headErr, string(out), string(headOut))
		}

		lines := strings.Split(strings.TrimSpace(string(headOut)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			parts := strings.Fields(lines[0])
			if len(parts) >= 1 {
				return parts[0], nil
			}
		}
		return "", fmt.Errorf("no HEAD commit found for %s", repo)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && lines[0] != "" {
		// Format: <sha>\trefs/tags/<tag>
		fields := strings.Fields(lines[0])
		if len(fields) >= 2 {
			tagRef := fields[1]
			// Strip "refs/tags/" prefix
			tagName := strings.TrimPrefix(tagRef, "refs/tags/")
			return tagName, nil
		}
	}

	return "", fmt.Errorf("no tags found for %s", repo)
}

// HasUpdate checks whether a newer version of the skill exists on the remote source.
// Returns (hasUpdate bool, newVersion string, error).
func HasUpdate(entry SkillEntry) (bool, string, error) {
	if entry.SourceURL == "" {
		return false, "", nil // no provenance tracked, cannot compare
	}

	remoteVer, err := CheckRemoteVersion(entry.SourceURL)
	if err != nil {
		return false, "", fmt.Errorf("check remote version for '%s': %w", entry.Name, err)
	}

	if remoteVer == "" {
		return false, "", nil // no tags available
	}

	// Simple string comparison: if remote version differs from installed version, update is available
	if remoteVer != entry.Version {
		return true, remoteVer, nil
	}

	return false, "", nil
}

// GetEntry looks up a skill entry by name from the base directory.
func GetEntry(name string, destBaseDir string) (*SkillEntry, error) {
	skillPath := filepath.Join(destBaseDir, name)
	entry, err := readSkillMetadata(skillPath)
	if err != nil {
		return nil, fmt.Errorf("skill '%s' not found at %s: %w", name, skillPath, err)
	}
	return &entry, nil
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
	fmContent := content[frontmatterStart+3 : frontmatterEnd]

	// Parse frontmatter into a generic map for flexible tool schema support.
	var rawMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(fmContent), &rawMap); err != nil {
		return SkillEntry{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	name, _ := rawMap["name"].(string)
	description, _ := rawMap["description"].(string)
	activation, _ := rawMap["activation"].(string)
	mcPEndpoint, _ := rawMap["mcp_endpoint"].(string)
	mcpCapabilities, _ := rawMap["mcp_capabilities"].([]interface{})
	sourceURL, _ := rawMap["source_url"].(string)
	version, _ := rawMap["version"].(string)
	binaryPath, _ := rawMap["binary_path"].(string)

	if name == "" {
		return SkillEntry{}, fmt.Errorf("missing 'name' in frontmatter")
	}

	activation = strings.ToLower(activation)
	if activation == "" {
		activation = "on-demand" // default
	}

	entry := SkillEntry{
		Name:        name,
		Description: description,
		Path:        dir,
		Activation:  activation,
	}

	// Parse tools — supports both string array (backward compat) and structured definitions.
	if toolsRaw, ok := rawMap["tools"].([]interface{}); ok {
		for _, item := range toolsRaw {
			switch v := item.(type) {
			case string:
				// Backward compatible: simple tool name string
				entry.Tools = append(entry.Tools, ToolDef{Name: v})
			case map[string]interface{}:
				// Structured definition with typed parameters
				td := ToolDef{
					Name:        v["name"].(string),
					Description: v["description"].(string),
				}
				if params, ok := v["parameters"].(map[string]interface{}); ok {
					td.Parameters = params
				}
				entry.Tools = append(entry.Tools, td)
			}
		}
	}

	// Set MCP fields if provided.
	if mcPEndpoint != "" {
		entry.MCPEndpoint = mcPEndpoint
		for _, cap := range mcpCapabilities {
			if s, ok := cap.(string); ok {
				entry.MCPCapabilities = append(entry.MCPCapabilities, s)
			}
		}
	}

	// Set provenance fields if present.
	if sourceURL != "" {
		entry.SourceURL = sourceURL
	}
	if version != "" {
		entry.Version = version
	}

	// Set binary path for pre-built binary skills.
	if binaryPath != "" {
		entry.BinaryPath = binaryPath
	}

	return entry, nil
}
