package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotifyConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.System.Notify.BellEnabled {
		t.Errorf("expected BellEnabled=true, got %v", cfg.System.Notify.BellEnabled)
	}
	if cfg.System.Notify.RateLimitPerMin != 10 {
		t.Errorf("expected RateLimitPerMin=10, got %d", cfg.System.Notify.RateLimitPerMin)
	}
	if cfg.System.Notify.SlackWebhook != "" {
		t.Errorf("expected empty SlackWebhook, got %q", cfg.System.Notify.SlackWebhook)
	}
}

func TestNotifyConfigSlackUrl(t *testing.T) {
	cfg := DefaultConfig()
	cfg.System.Notify.SlackWebhook = "https://hooks.slack.com/services/T00/B00/XXXX"
	if cfg.System.Notify.SlackWebhook != "https://hooks.slack.com/services/T00/B00/XXXX" {
		t.Errorf("expected SlackWebhook to be stored, got %q", cfg.System.Notify.SlackWebhook)
	}
}

func TestNotifyConfigValidationGood(t *testing.T) {
	cfg := DefaultConfig()
	cfg.System.Notify.RateLimitPerMin = 5
	// Set api_key on the openai provider so validation passes
	if len(cfg.System.LlmProviders) > 0 && cfg.System.LlmProviders[0].Type == "openai" {
		cfg.System.LlmProviders[0].ApiKey = "sk-test-key-123"
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestNotifyConfigValidationZero(t *testing.T) {
	cfg := DefaultConfig()
	cfg.System.Notify.RateLimitPerMin = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for rate_limit_per_minute=0")
	}
	expected := "system.notify.rate_limit_per_minute must be >= 1, got 0"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestNotifyConfigValidationNegative(t *testing.T) {
	cfg := DefaultConfig()
	cfg.System.Notify.RateLimitPerMin = -3
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for rate_limit_per_minute=-3")
	}
	expected := "system.notify.rate_limit_per_minute must be >= 1, got -3"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestNotifyConfigYAMLUnmarshal(t *testing.T) {
	yaml := `system:
  notify:
    bell_enabled: false
    slack_webhook: "https://hooks.slack.com/services/T123/B456/xyz"
    rate_limit_per_minute: 20`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test_config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.System.Notify.BellEnabled {
		t.Errorf("expected BellEnabled=false from YAML, got true")
	}
	if cfg.System.Notify.SlackWebhook != "https://hooks.slack.com/services/T123/B456/xyz" {
		t.Errorf("expected SlackWebhook from YAML, got %q", cfg.System.Notify.SlackWebhook)
	}
	if cfg.System.Notify.RateLimitPerMin != 20 {
		t.Errorf("expected RateLimitPerMin=20 from YAML, got %d", cfg.System.Notify.RateLimitPerMin)
	}

	// Set api_key on the openai provider so validation passes
	if len(cfg.System.LlmProviders) > 0 && cfg.System.LlmProviders[0].Type == "openai" {
		cfg.System.LlmProviders[0].ApiKey = "sk-test-key-123"
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed: %v", err)
	}
}

func TestNotifyConfigYAMLDefaults(t *testing.T) {
	yaml := `system:
  notify:
    bell_enabled: true`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test_config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Missing rate_limit_per_minute should default to 10
	if cfg.System.Notify.RateLimitPerMin != 10 {
		t.Errorf("expected RateLimitPerMin default=10, got %d", cfg.System.Notify.RateLimitPerMin)
	}
	// Missing slack_webhook should be empty
	if cfg.System.Notify.SlackWebhook != "" {
		t.Errorf("expected empty SlackWebhook, got %q", cfg.System.Notify.SlackWebhook)
	}
}

func TestDefaultHost(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.System.Host != "127.0.0.1" {
		t.Errorf("expected Host=127.0.0.1, got %q", cfg.System.Host)
	}
}

func TestEnvVarPortOverride(t *testing.T) {
	os.Setenv("PORT", "9090")
	defer os.Unsetenv("PORT")

	cfg := DefaultConfig()
	overrideEnvVars(cfg.System)
	if cfg.System.ServerPort != 9090 {
		t.Errorf("expected ServerPort=9090 from env, got %d", cfg.System.ServerPort)
	}
}

func TestEnvVarHostOverride(t *testing.T) {
	os.Setenv("HOST", "0.0.0.0")
	defer os.Unsetenv("HOST")

	cfg := DefaultConfig()
	overrideEnvVars(cfg.System)
	if cfg.System.Host != "0.0.0.0" {
		t.Errorf("expected Host=0.0.0.0 from env, got %q", cfg.System.Host)
	}
}

func TestWorkspaceConfigMerge(t *testing.T) {
	cfg := DefaultConfig()

	// Set a workspace override
	cfg.Workspaces["test-ws"] = &WorkspaceConfig{
		Model:          "claude-sonnet",
		MaxTokens:      64000,
		Temperature:      0.3,
		SkillOverrides: []string{"web-search", "git"},
	}

	merged := cfg.MergeWorkspaceConfig("test-ws", cfg.Workspaces["test-ws"])

	if merged.Model != "claude-sonnet" {
		t.Errorf("expected model=claude-sonnet, got %q", merged.Model)
	}
	if merged.MaxTokens != 64000 {
		t.Errorf("expected max_tokens=64000, got %d", merged.MaxTokens)
	}
	if merged.Temperature != 0.3 {
		t.Errorf("expected temperature=0.3, got %.2f", merged.Temperature)
	}
	if len(merged.SkillOverrides) != 2 {
		t.Errorf("expected 2 skill overrides, got %d", len(merged.SkillOverrides))
	}
}

func TestWorkspaceConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	ws := &WorkspaceConfig{}

	merged := cfg.MergeWorkspaceConfig("empty-ws", ws)

	if merged.Model != "gpt-4o" {
		t.Errorf("expected model to fall back to provider default 'gpt-4o', got %q", merged.Model)
	}
	if merged.Temperature != 0.7 {
		t.Errorf("expected temperature to fall back to 0.7, got %.2f", merged.Temperature)
	}
}
