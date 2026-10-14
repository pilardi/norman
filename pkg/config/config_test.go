package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotifyConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Notify.BellEnabled {
		t.Errorf("expected BellEnabled=true, got %v", cfg.Notify.BellEnabled)
	}
	if cfg.Notify.RateLimitPerMin != 10 {
		t.Errorf("expected RateLimitPerMin=10, got %d", cfg.Notify.RateLimitPerMin)
	}
	if cfg.Notify.SlackWebhook != "" {
		t.Errorf("expected empty SlackWebhook, got %q", cfg.Notify.SlackWebhook)
	}
}

func TestNotifyConfigSlackUrl(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notify.SlackWebhook = "https://hooks.slack.com/services/T00/B00/XXXX"
	if cfg.Notify.SlackWebhook != "https://hooks.slack.com/services/T00/B00/XXXX" {
		t.Errorf("expected SlackWebhook to be stored, got %q", cfg.Notify.SlackWebhook)
	}
}

func TestNotifyConfigValidationGood(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notify.RateLimitPerMin = 5
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestNotifyConfigValidationZero(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notify.RateLimitPerMin = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for rate_limit_per_minute=0")
	}
	expected := "notify.rate_limit_per_minute must be >= 1, got 0"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestNotifyConfigValidationNegative(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notify.RateLimitPerMin = -3
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for rate_limit_per_minute=-3")
	}
	expected := "notify.rate_limit_per_minute must be >= 1, got -3"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestNotifyConfigYAMLUnmarshal(t *testing.T) {
	yaml := `notify:
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

	if cfg.Notify.BellEnabled {
		t.Errorf("expected BellEnabled=false from YAML, got true")
	}
	if cfg.Notify.SlackWebhook != "https://hooks.slack.com/services/T123/B456/xyz" {
		t.Errorf("expected SlackWebhook from YAML, got %q", cfg.Notify.SlackWebhook)
	}
	if cfg.Notify.RateLimitPerMin != 20 {
		t.Errorf("expected RateLimitPerMin=20 from YAML, got %d", cfg.Notify.RateLimitPerMin)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed: %v", err)
	}
}

func TestNotifyConfigYAMLDefaults(t *testing.T) {
	yaml := `notify:
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
	if cfg.Notify.RateLimitPerMin != 10 {
		t.Errorf("expected RateLimitPerMin default=10, got %d", cfg.Notify.RateLimitPerMin)
	}
	// Missing slack_webhook should be empty
	if cfg.Notify.SlackWebhook != "" {
		t.Errorf("expected empty SlackWebhook, got %q", cfg.Notify.SlackWebhook)
	}
}
