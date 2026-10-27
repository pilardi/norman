// Package config provides server-driven configuration parsing, validation,
// and defaults for Norman. Configuration is now derived from server interaction
// rather than assumed to exist on the local filesystem.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SystemConfig holds global defaults shared across all workspaces.
type SystemConfig struct {
	ServerPort int               `yaml:"server_port"`
	Host       string            `yaml:"host"` // bind address for server (default 127.0.0.1)
	BaseDir    string            `yaml:"base_dir"`
	Skills     SkillsConfig      `yaml:"skills"`
	Notify     NotifyConfig      `yaml:"notify"`
	Logging    LoggingConfig     `yaml:"logging"`
	OpenShell  OpenShellConfig   `yaml:"openshell"` // OpenShell gateway connection config

	// LLM provider configuration.
	LlmProviders []ProviderConfig `yaml:"llm_providers"`

	// ServerURL is the external-facing URL for the Norman API (e.g., https://norman.example.com).
	// If set, it overrides the auto-derived http://host:port URL used in context files and agent communication.
	ServerURL string `yaml:"server_url,omitempty"`
}

// ProviderConfig describes a named LLM/embedder provider endpoint.
type ProviderConfig struct {
	Name      string         `yaml:"name"`       // identifier, e.g. "default", "local"
	Type      string         `yaml:"type"`       // "openai", "llamacpp", etc.
	Endpoint  string         `yaml:"endpoint"`   // base API URL
	Model     string         `yaml:"model"`      // model name/identifier
	Temperature float64        `yaml:"temperature"` // provider default temperature
	MaxTokens int            `yaml:"max_tokens"`  // provider default max tokens
	ApiKey    string         `yaml:"api_key"`     // provider API key (e.g. OpenAI key)
	Extra     map[string]any `yaml:",inline"`    // type-specific fields (cache_size, etc.)
}

// ResolveProvider returns the provider config matching the given name.
func (sc *SystemConfig) ResolveProvider(name string) (*ProviderConfig, error) {
	for i := range sc.LlmProviders {
		if sc.LlmProviders[i].Name == name {
			return &sc.LlmProviders[i], nil
		}
	}
	return nil, fmt.Errorf("provider %q not found in configured providers", name)
}

// WorkspaceConfig holds per-workspace overrides.
// Each workspace can override model, token budget, skill set, etc.
type WorkspaceConfig struct {
	BaseDir        string   `yaml:"base_dir"`         // e.g., "~/workspaces" or derived from environment
	Model          string   `yaml:"model"`            // per-workspace model (empty = use system default)
	MaxTokens      int      `yaml:"max_tokens"`       // per-workspace token budget
	Temperature    float64  `yaml:"temperature"`      // per-workspace temperature
	SkillOverrides []string `yaml:"skill_overrides"`  // per-workspace skill activation list
	SystemPrompt   string   `yaml:"system_prompt"`    // per-workspace system prompt override
}

// Config is the top-level configuration returned by the server.
// It merges SystemConfig defaults with per-workspace overrides.
type Config struct {
	System     *SystemConfig  `yaml:"system"`
	Workspaces map[string]*WorkspaceConfig `yaml:"workspaces,omitempty"` // workspace_id -> WorkspaceConfig
}

// SkillsConfig holds skill-related settings.
type SkillsConfig struct {
	DiscoveryPaths []string `yaml:"discovery_paths"` // paths to scan for SKILL.md files
	MaxActive      int      `yaml:"max_active"`     // max concurrent active skills (0 = unlimited)
	Path           string   `yaml:"path"`           // base directory where uploaded skills are stored
}

// NotifyConfig holds notification delivery settings.
type NotifyConfig struct {
	BellEnabled      bool    `yaml:"bell_enabled"`       // ring terminal bell on notifications
	SlackWebhook     string  `yaml:"slack_webhook"`      // optional Slack webhook URL
	RateLimitPerMin  int     `yaml:"rate_limit_per_minute"` // max notifications per minute (default 10)
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Verbose bool `yaml:"verbose"` // enable verbose/debug output
}

// OpenShellConfig holds OpenShell gateway connection settings.
type OpenShellConfig struct {
	Address string `yaml:"address"` // explicit gateway address (e.g., "127.0.0.1:8090")
}

// DefaultSystemConfig returns a SystemConfig with embedded defaults applied.
func DefaultSystemConfig() *SystemConfig {
	return EmbeddedDefaults().System
}

// DefaultWorkspaceConfig returns a WorkspaceConfig with embedded defaults applied.
func DefaultWorkspaceConfig() *WorkspaceConfig {
	return &WorkspaceConfig{
		BaseDir:        EmbeddedDefaults().System.BaseDir,
		Model:          "",             // empty = use system default
		MaxTokens:      0,              // zero = use system default
		Temperature:    0,              // zero = use system default
		SkillOverrides: []string{},     // empty = use all discovered skills
		SystemPrompt:   "",             // empty = use system default
	}
}

// DefaultConfig returns a Config with sensible defaults applied.
func DefaultConfig() *Config {
	return &Config{
		System:     DefaultSystemConfig(),
		Workspaces: make(map[string]*WorkspaceConfig),
	}
}

// Load reads config from a file path and returns it with defaults merged in.
func Load(path string) (*Config, error) {
	c := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Apply defaults for zero-valued fields in SystemConfig
	if c.System.ServerPort == 0 {
		c.System.ServerPort = EmbeddedDefaults().System.ServerPort
	}
	if c.System.Skills.MaxActive == 0 {
		c.System.Skills.MaxActive = EmbeddedDefaults().System.Skills.MaxActive
	}

	return c, nil
}

// LoadDefault reads from the standard config path (~/.norman/config.yaml).
// If the file does not exist, returns DefaultConfig() instead of an error.
// Also loads .env files and applies NORMAN_* environment variable overrides.
//
// Priority order (highest to lowest):
//   1. NORMAN_* environment variables (read directly from os.Environ)
//   2. .env files (current dir, then $HOME/.norman/.env)
//   3. config.yaml
//   4. Embedded defaults
func LoadDefault() (*Config, error) {
	// Step 1: Load .env files (lower priority than env vars).
	if err := LoadEnvFiles(); err != nil {
		// Non-fatal: log warning but continue with embedded defaults.
		fmt.Fprintf(os.Stderr, "[config] WARNING: failed to load .env files: %v\n", err)
	}

	configPath := os.ExpandEnv("$HOME/.norman/config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	path := configPath

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c := EmbeddedDefaults()
			if err := ApplyEnvOverrides(c.System); err != nil {
				fmt.Fprintf(os.Stderr, "[config] WARNING: %v\n", err)
			}
			return c, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	c := DefaultConfig()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Apply defaults for zero-valued fields
	if c.System.ServerPort == 0 {
		c.System.ServerPort = EmbeddedDefaults().System.ServerPort
	}
	if c.System.Skills.MaxActive == 0 {
		c.System.Skills.MaxActive = EmbeddedDefaults().System.Skills.MaxActive
	}

	// Step 2: Apply NORMAN_* env var overrides (highest priority).
	if err := ApplyEnvOverrides(c.System); err != nil {
		fmt.Fprintf(os.Stderr, "[config] WARNING: %v\n", err)
	}

	return c, nil
}

// Save writes the Config back to a file path.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Validate checks required fields and returns errors.
func (c *Config) Validate() error {
	if c.System.ServerPort < 1 || c.System.ServerPort > 65535 {
		return fmt.Errorf("system.server_port must be between 1 and 65535, got %d", c.System.ServerPort)
	}
	if c.System.Skills.MaxActive < 0 {
		return fmt.Errorf("system.skills.max_active must be >= 0, got %d", c.System.Skills.MaxActive)
	}
	if c.System.Notify.RateLimitPerMin < 1 {
		return fmt.Errorf("system.notify.rate_limit_per_minute must be >= 1, got %d", c.System.Notify.RateLimitPerMin)
	}

	// Validate providers
	for i, p := range c.System.LlmProviders {
		if p.Name == "" {
			return fmt.Errorf("llm_providers[%d]: name is required", i)
		}
		if p.Type == "" {
			return fmt.Errorf("llm_providers[%d].%s: type is required", i, p.Name)
		}
		if p.Model == "" {
			return fmt.Errorf("llm_providers[%d].%s: model is required", i, p.Name)
		}
		if p.Type == "openai" && p.ApiKey == "" {
			return fmt.Errorf("llm_providers[%d].%s: api_key is required for openai providers", i, p.Name)
		}
		if p.Temperature < 0 || p.Temperature > 2 {
			return fmt.Errorf("llm_providers[%d].%s: temperature must be between 0 and 2, got %.2f", i, p.Name, p.Temperature)
		}
		if p.MaxTokens < 1 {
			return fmt.Errorf("llm_providers[%d].%s: max_tokens must be >= 1, got %d", i, p.Name, p.MaxTokens)
		}
	}

	// Validate per-workspace configs
	for wsID, ws := range c.Workspaces {
		if ws.MaxTokens > 0 && ws.MaxTokens < 1024 {
			return fmt.Errorf("workspace %s: max_tokens must be >= 1024, got %d", wsID, ws.MaxTokens)
		}
		if ws.Temperature != 0 && (ws.Temperature < 0 || ws.Temperature > 2) {
			return fmt.Errorf("workspace %s: temperature must be between 0 and 2, got %.2f", wsID, ws.Temperature)
		}
	}

	return nil
}

// MergeWorkspaceConfig merges a workspace config into the system defaults.
// Returns a resolved WorkspaceConfig with all zero-values filled from the first provider's defaults.
func (c *Config) MergeWorkspaceConfig(workspaceID string, ws *WorkspaceConfig) *WorkspaceConfig {
	result := DefaultWorkspaceConfig()

	// Use the first configured provider as the system-level default source.
	if len(c.System.LlmProviders) > 0 {
		first := c.System.LlmProviders[0]
		if result.Model == "" {
			result.Model = first.Model
		}
		if result.Temperature == 0 && first.Temperature > 0 {
			result.Temperature = first.Temperature
		}
		if result.MaxTokens == 0 && first.MaxTokens > 0 {
			result.MaxTokens = first.MaxTokens
		}
	}

	// If no workspace-level overrides are provided, return the defaults immediately.
	if ws == nil {
		return result
	}

	// Workspace-level overrides take precedence
	if ws.Model != "" {
		result.Model = ws.Model
	}
	if ws.MaxTokens > 0 {
		result.MaxTokens = ws.MaxTokens
	}
	if ws.Temperature > 0 {
		result.Temperature = ws.Temperature
	}
	if len(ws.SkillOverrides) > 0 {
		result.SkillOverrides = ws.SkillOverrides
	}
	if ws.SystemPrompt != "" {
		result.SystemPrompt = ws.SystemPrompt
	}

	return result
}

// SaveWorkspaceConfig writes a workspace config to the server-side registry.
func (c *Config) SaveWorkspaceConfig(workspaceID string, path string) error {
	data, err := yaml.Marshal(c.Workspaces[workspaceID])
	if err != nil {
		return fmt.Errorf("marshal workspace config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// LoadWorkspaceConfig reads a workspace config from the server-side registry.
func LoadWorkspaceConfig(path string) (*WorkspaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workspace config: %w", err)
	}

	ws := DefaultWorkspaceConfig()
	if err := yaml.Unmarshal(data, ws); err != nil {
		return nil, fmt.Errorf("parse workspace config: %w", err)
	}

	return ws, nil
}
