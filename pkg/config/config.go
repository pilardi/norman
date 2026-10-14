// Package config provides YAML-based configuration parsing, validation, and defaults for Norman.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level Norman configuration.
type Config struct {
	DefaultModel string            `yaml:"default_model"`
	ServerPort   int               `yaml:"server_port"`
	Skills       SkillsConfig      `yaml:"skills"`
	Workspaces   WorkspaceConfig   `yaml:"workspaces"`
	Agent        AgentConfig       `yaml:"agent"`
	Notify       NotifyConfig      `yaml:"notify"`
}

// SkillsConfig holds skill-related settings.
type SkillsConfig struct {
	DiscoveryPaths []string `yaml:"discovery_paths"` // paths to scan for SKILL.md files
	MaxActive      int      `yaml:"max_active"`     // max concurrent active skills (0 = unlimited)
}

// WorkspaceConfig holds workspace-related settings.
type WorkspaceConfig struct {
	BaseDir string `yaml:"base_dir"` // e.g., ~/workspaces or $HOME/workspaces
}

// AgentConfig holds agent-specific settings.
type AgentConfig struct {
	MaxTokens     int    `yaml:"max_tokens"`
	Temperature   float64 `yaml:"temperature"`
	SystemPrompt  string `yaml:"system_prompt"`
}

// NotifyConfig holds notification delivery settings.
type NotifyConfig struct {
	BellEnabled      bool    `yaml:"bell_enabled"`       // ring terminal bell on notifications
	SlackWebhook     string  `yaml:"slack_webhook"`      // optional Slack webhook URL
	RateLimitPerMin  int     `yaml:"rate_limit_per_minute"` // max notifications per minute (default 10)
}

const (
	DefaultModel       = "gpt-4o"
	DefaultServerPort  = 8080
	DefaultBaseDir     = "~/workspaces"
	DefaultMaxTokens   = 32000
	DefaultTemperature = 0.7
	DefaultMaxActive   = 3
	DefaultBellEnabled = true
	DefaultRateLimit   = 10
)

// DefaultConfig returns a Config with sensible defaults applied.
func DefaultConfig() *Config {
	return &Config{
		DefaultModel: DefaultModel,
		ServerPort:   DefaultServerPort,
		Skills: SkillsConfig{
			DiscoveryPaths: []string{
				"~/.norman/skills/",
				"/usr/share/norman/skills/",
			},
			MaxActive: DefaultMaxActive,
		},
		Workspaces: WorkspaceConfig{
			BaseDir: DefaultBaseDir,
		},
		Agent: AgentConfig{
			MaxTokens:    DefaultMaxTokens,
			Temperature:  DefaultTemperature,
			SystemPrompt: "",
		},
		Notify: NotifyConfig{
			BellEnabled:     DefaultBellEnabled,
			RateLimitPerMin: DefaultRateLimit,
		},
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

	// Apply defaults for zero-valued fields
	if c.DefaultModel == "" {
		c.DefaultModel = DefaultModel
	}
	if c.ServerPort == 0 {
		c.ServerPort = DefaultServerPort
	}
	if c.Workspaces.BaseDir == "" {
		c.Workspaces.BaseDir = DefaultBaseDir
	}
	if c.Agent.MaxTokens == 0 {
		c.Agent.MaxTokens = DefaultMaxTokens
	}
	if c.Agent.Temperature == 0 {
		c.Agent.Temperature = DefaultTemperature
	}
	if c.Skills.MaxActive == 0 {
		c.Skills.MaxActive = DefaultMaxActive
	}

	return c, nil
}

// LoadDefault reads from the standard config path (~/.config/norman/config.yaml).
// If the file does not exist, returns DefaultConfig() instead of an error.
func LoadDefault() (*Config, error) {
	configDir := os.ExpandEnv("$HOME/.config/norman")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	path := filepath.Join(configDir, "config.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	c := DefaultConfig()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Apply defaults for zero-valued fields
	if c.DefaultModel == "" {
		c.DefaultModel = DefaultModel
	}
	if c.ServerPort == 0 {
		c.ServerPort = DefaultServerPort
	}
	if c.Workspaces.BaseDir == "" {
		c.Workspaces.BaseDir = DefaultBaseDir
	}
	if c.Agent.MaxTokens == 0 {
		c.Agent.MaxTokens = DefaultMaxTokens
	}
	if c.Agent.Temperature == 0 {
		c.Agent.Temperature = DefaultTemperature
	}
	if c.Skills.MaxActive == 0 {
		c.Skills.MaxActive = DefaultMaxActive
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
	if c.DefaultModel == "" {
		return fmt.Errorf("default_model is required")
	}
	if c.ServerPort < 1 || c.ServerPort > 65535 {
		return fmt.Errorf("server_port must be between 1 and 65535, got %d", c.ServerPort)
	}
	if c.Agent.MaxTokens < 1024 {
		return fmt.Errorf("max_tokens must be >= 1024, got %d", c.Agent.MaxTokens)
	}
	if c.Agent.Temperature < 0 || c.Agent.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2, got %.2f", c.Agent.Temperature)
	}
	if c.Skills.MaxActive < 0 {
		return fmt.Errorf("skills.max_active must be >= 0, got %d", c.Skills.MaxActive)
	}
	if c.Notify.RateLimitPerMin < 1 {
		return fmt.Errorf("notify.rate_limit_per_minute must be >= 1, got %d", c.Notify.RateLimitPerMin)
	}
	return nil
}
