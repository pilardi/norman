// Package config provides server-driven configuration parsing, validation,
// and defaults for Norman. Configuration is now derived from server interaction
// rather than assumed to exist on the local filesystem.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// SystemConfig holds global defaults shared across all workspaces.
type SystemConfig struct {
	DefaultModel string            `yaml:"default_model"`
	ServerPort   int               `yaml:"server_port"`
	Host         string            `yaml:"host"` // bind address for server (default 127.0.0.1)
	Skills       SkillsConfig      `yaml:"skills"`
	Notify       NotifyConfig      `yaml:"notify"`
	Logging      LoggingConfig     `yaml:"logging"`
	Temperature    float64           `yaml:"temperature"` // global default temperature (0 = use default)
	MaxTokens    int               `yaml:"max_tokens"`  // global default max tokens (0 = use system default)
	OpenShell    OpenShellConfig   `yaml:"openshell"`   // OpenShell gateway connection config
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

const (
	DefaultModel       = "gpt-4o"
	DefaultServerPort  = 8080
	DefaultHost        = "127.0.0.1"
	DefaultBaseDir     = "~/workspaces"
	DefaultMaxTokens   = 32000
	DefaultTemperature = 0.7
	DefaultMaxActive   = 3
	DefaultBellEnabled = true
	DefaultRateLimit   = 10
)

var (
	defaultTemperature float64 = 0.7
	defaultMaxTokens   int     = 32000
)

// DefaultSystemConfig returns a SystemConfig with sensible defaults applied.
func DefaultSystemConfig() *SystemConfig {
	return &SystemConfig{
		DefaultModel:  DefaultModel,
		ServerPort:    DefaultServerPort,
		Host:          DefaultHost,
		Temperature:     defaultTemperature,
		MaxTokens:     defaultMaxTokens,
		OpenShell: OpenShellConfig{
			Address: "", // empty = auto-resolve from env var or metadata.json
		},
		Skills: SkillsConfig{
			DiscoveryPaths: []string{
				"~/.norman/skills/",
				"/usr/share/norman/skills/",
			},
			MaxActive: DefaultMaxActive,
		},
		Notify: NotifyConfig{
			BellEnabled:     DefaultBellEnabled,
			RateLimitPerMin: DefaultRateLimit,
		},
		Logging: LoggingConfig{
			Verbose: false, // disabled by default; use --verbose flag or config
		},
	}
}

// DefaultWorkspaceConfig returns a WorkspaceConfig with defaults applied.
func DefaultWorkspaceConfig() *WorkspaceConfig {
	return &WorkspaceConfig{
		BaseDir:        DefaultBaseDir,
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
	if c.System.DefaultModel == "" {
		c.System.DefaultModel = DefaultModel
	}
	if c.System.ServerPort == 0 {
		c.System.ServerPort = DefaultServerPort
	}
	if c.System.Skills.MaxActive == 0 {
		c.System.Skills.MaxActive = DefaultMaxActive
	}

	return c, nil
}

// LoadDefault reads from the standard config path (~/.config/norman/config.yaml).
// If the file does not exist, returns DefaultConfig() instead of an error.
// Also loads .env files from the current directory and $HOME/.norman/.env as fallbacks.
func LoadDefault() (*Config, error) {
	// Try loading .env files (current dir first, then home dir)
	envDir := os.Getenv("HOME")
	if envDir == "" {
		envDir = "."
	}
	godotenv.Load(".env", filepath.Join(envDir, ".norman", ".env"))

	configDir := os.ExpandEnv("$HOME/.config/norman")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	path := filepath.Join(configDir, "config.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c := DefaultConfig()
			overrideEnvVars(c.System)
			return c, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	c := DefaultConfig()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Apply defaults for zero-valued fields
	if c.System.DefaultModel == "" {
		c.System.DefaultModel = DefaultModel
	}
	if c.System.ServerPort == 0 {
		c.System.ServerPort = DefaultServerPort
	}
	if c.System.Skills.MaxActive == 0 {
		c.System.Skills.MaxActive = DefaultMaxActive
	}

	overrideEnvVars(c.System)
	return c, nil
}

// overrideEnvVars applies environment variable overrides to the system config.
// Priority: env vars > YAML file > defaults.
func overrideEnvVars(sc *SystemConfig) {
	if port := os.Getenv("PORT"); port != "" {
		n, err := fmt.Sscanf(port, "%d", &sc.ServerPort)
		if n != 1 || err != nil {
			fmt.Fprintf(os.Stderr, "[config] WARNING: invalid PORT value %q, ignoring\n", port)
		}
	}
	if host := os.Getenv("HOST"); host != "" {
		sc.Host = host
	}
	if v := os.Getenv("LOGGING_VERBOSE"); v != "" {
		sc.Logging.Verbose = (v == "true" || v == "1")
	}
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
	if c.System.DefaultModel == "" {
		return fmt.Errorf("system.default_model is required")
	}
	if c.System.ServerPort < 1 || c.System.ServerPort > 65535 {
		return fmt.Errorf("system.server_port must be between 1 and 65535, got %d", c.System.ServerPort)
	}
	if c.System.Skills.MaxActive < 0 {
		return fmt.Errorf("system.skills.max_active must be >= 0, got %d", c.System.Skills.MaxActive)
	}
	if c.System.Notify.RateLimitPerMin < 1 {
		return fmt.Errorf("system.notify.rate_limit_per_minute must be >= 1, got %d", c.System.Notify.RateLimitPerMin)
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
// Returns a resolved WorkspaceConfig with all zero-values filled from system defaults.
func (c *Config) MergeWorkspaceConfig(workspaceID string, ws *WorkspaceConfig) *WorkspaceConfig {
	result := DefaultWorkspaceConfig()

	// System-level overrides (these are the global defaults)
	if c.System.DefaultModel != "" && ws.Model == "" {
		result.Model = c.System.DefaultModel
	}
	if ws.MaxTokens == 0 {
		result.MaxTokens = c.System.MaxTokens
	}
	if ws.Temperature == 0 {
		result.Temperature = c.System.Temperature
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
