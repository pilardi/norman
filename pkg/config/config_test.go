package config

import (
	"os"
	"path/filepath"
	"strings"
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
	os.Setenv("NORMAN_PORT", "9090")
	defer os.Unsetenv("NORMAN_PORT")

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if cfg.System.ServerPort != 9090 {
		t.Errorf("expected ServerPort=9090 from env, got %d", cfg.System.ServerPort)
	}
}

func TestEnvVarHostOverride(t *testing.T) {
	os.Setenv("NORMAN_HOST", "0.0.0.0")
	defer os.Unsetenv("NORMAN_HOST")

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if cfg.System.Host != "0.0.0.0" {
		t.Errorf("expected Host=0.0.0.0 from env, got %q", cfg.System.Host)
	}
}

func TestEnvVarServerURLOverride(t *testing.T) {
	os.Setenv("NORMAN_SERVER_URL", "https://norman.example.com")
	defer os.Unsetenv("NORMAN_SERVER_URL")

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if cfg.System.ServerURL != "https://norman.example.com" {
		t.Errorf("expected ServerURL=https://norman.example.com, got %q", cfg.System.ServerURL)
	}
}

func TestEnvVarVerboseOverride(t *testing.T) {
	os.Setenv("NORMAN_VERBOSE", "true")
	defer os.Unsetenv("NORMAN_VERBOSE")

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if !cfg.System.Logging.Verbose {
		t.Errorf("expected Logging.Verbose=true, got %v", cfg.System.Logging.Verbose)
	}
}

func TestEnvVarVerboseFalse(t *testing.T) {
	os.Setenv("NORMAN_VERBOSE", "false")
	defer os.Unsetenv("NORMAN_VERBOSE")

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if cfg.System.Logging.Verbose {
		t.Errorf("expected Logging.Verbose=false, got %v", cfg.System.Logging.Verbose)
	}
}

func TestEnvVarBaseDirOverride(t *testing.T) {
	os.Setenv("NORMAN_BASE_DIR", "/custom/workspaces")
	defer os.Unsetenv("NORMAN_BASE_DIR")

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if cfg.System.BaseDir != "/custom/workspaces" {
		t.Errorf("expected BaseDir=/custom/workspaces, got %q", cfg.System.BaseDir)
	}
}

func TestEnvVarInvalidPort(t *testing.T) {
	os.Setenv("NORMAN_PORT", "not-a-number")
	defer os.Unsetenv("NORMAN_PORT")

	cfg := DefaultConfig()
	err := ApplyEnvOverrides(cfg.System)
	if err == nil {
		t.Fatal("expected error for invalid NORMAN_PORT value")
	}
	if !strings.Contains(err.Error(), "invalid NORMAN_PORT value") {
		t.Errorf("expected 'invalid NORMAN_PORT' in error, got: %v", err)
	}
}

func TestEnvVarInvalidVerbose(t *testing.T) {
	os.Setenv("NORMAN_VERBOSE", "maybe")
	defer os.Unsetenv("NORMAN_VERBOSE")

	cfg := DefaultConfig()
	err := ApplyEnvOverrides(cfg.System)
	if err != nil {
		t.Fatalf("ApplyEnvOverrides should not fail for 'maybe' (treated as false): %v", err)
	}
	if cfg.System.Logging.Verbose {
		t.Errorf("expected Logging.Verbose=false, got %v", cfg.System.Logging.Verbose)
	}
}

func TestEnvVarPriority(t *testing.T) {
	// Set env vars that should override defaults
	os.Setenv("NORMAN_PORT", "9090")
	os.Setenv("NORMAN_HOST", "0.0.0.0")
	defer func() {
		os.Unsetenv("NORMAN_PORT")
		os.Unsetenv("NORMAN_HOST")
	}()

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}
	if cfg.System.ServerPort != 9090 {
		t.Errorf("expected ServerPort=9090, got %d", cfg.System.ServerPort)
	}
	if cfg.System.Host != "0.0.0.0" {
		t.Errorf("expected Host=0.0.0.0, got %q", cfg.System.Host)
	}
}

func TestEnvVarOverridesDotEnv(t *testing.T) {
	tmpDir := t.TempDir()
	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer func() {
		os.Chdir(oldCwd)
	}()

	// Write a .env file with PORT=7070
	envContent := "NORMAN_PORT=7070\nNORMAN_HOST=192.168.1.1\n"
	if err := os.WriteFile(".env", []byte(envContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Set env var that should override the .env value
	os.Setenv("NORMAN_PORT", "8888")
	defer os.Unsetenv("NORMAN_PORT")

	if err := LoadEnvFiles(); err != nil {
		t.Fatalf("LoadEnvFiles failed: %v", err)
	}

	cfg := DefaultConfig()
	if err := ApplyEnvOverrides(cfg.System); err != nil {
		t.Fatalf("ApplyEnvOverrides failed: %v", err)
	}

	// Env var should override .env value
	if cfg.System.ServerPort != 8888 {
		t.Errorf("expected ServerPort=8888 (from env var), got %d", cfg.System.ServerPort)
	}
	// .env value should be applied for un-overridden vars
	if cfg.System.Host != "192.168.1.1" {
		t.Errorf("expected Host=192.168.1.1 (from .env), got %q", cfg.System.Host)
	}

	// Cleanup
	os.Remove(".env")
}

func TestGetEnvVarNames(t *testing.T) {
	names := GetEnvVarNames()
	if len(names) == 0 {
		t.Fatal("expected at least one env var name")
	}
	// Check that all names have NORMAN_ prefix
	for _, name := range names {
		if !strings.HasPrefix(name, "NORMAN_") {
			t.Errorf("expected name to start with 'NORMAN_', got %q", name)
		}
	}
}

func TestIsNORMANPrefix(t *testing.T) {
	if !isNORMANPrefix("NORMAN_HOST") {
		t.Error("expected isNORMANPrefix('NORMAN_HOST') to be true")
	}
	if isNORMANPrefix("PORT") {
		t.Error("expected isNORMANPrefix('PORT') to be false")
	}
	if !isNORMANPrefix("NORMAN_BASE_DIR") {
		t.Error("expected isNORMANPrefix('NORMAN_BASE_DIR') to be true")
	}
}

func TestBuildEnvVarHelpText(t *testing.T) {
	help := BuildEnvVarHelpText()
	if help == "" {
		t.Fatal("expected non-empty help text")
	}
	if !strings.Contains(help, "NORMAN_HOST") {
		t.Error("expected help to contain NORMAN_HOST")
	}
	if !strings.Contains(help, "NORMAN_PORT") {
		t.Error("expected help to contain NORMAN_PORT")
	}
	if !strings.Contains(help, "NORMAN_SERVER_URL") {
		t.Error("expected help to contain NORMAN_SERVER_URL")
	}
}

func TestGetEnvVarHelp(t *testing.T) {
	help := GetEnvVarHelp("NORMAN_HOST")
	if help == "" {
		t.Fatal("expected non-empty help for NORMAN_HOST")
	}
	if !strings.Contains(help, "Server bind address") {
		t.Error("expected help to describe server bind address")
	}

	help = GetEnvVarHelp("NORMAN_PORT")
	if help == "" {
		t.Fatal("expected non-empty help for NORMAN_PORT")
	}
	if !strings.Contains(help, "Server port number") {
		t.Error("expected help to describe server port")
	}

	// Unknown name should return empty string
	help = GetEnvVarHelp("UNKNOWN_VAR")
	if help != "" {
		t.Errorf("expected empty help for unknown var, got %q", help)
	}
}

func TestWorkspaceConfigMerge(t *testing.T) {
	cfg := DefaultConfig()

	// Set a workspace override
	cfg.Workspaces["test-ws"] = &WorkspaceConfig{
		Model:          "claude-sonnet",
		MaxTokens:      64000,
		Temperature:    0.3,
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

	if merged.Model != "unsloth/Qwen3.8-27B-GGUF:IQ3_S" {
		t.Errorf("expected model to fall back to provider default 'unsloth/Qwen3.8-27B-GGUF:IQ3_S', got %q", merged.Model)
	}
	if merged.Temperature != 0.7 {
		t.Errorf("expected temperature to fall back to 0.7, got %.2f", merged.Temperature)
	}
}

func TestMergeWorkspaceConfigNil(t *testing.T) {
	cfg := DefaultConfig()

	// Passing nil should not panic and should return defaults.
	merged := cfg.MergeWorkspaceConfig("nil-ws", nil)

	if merged.Model != "unsloth/Qwen3.8-27B-GGUF:IQ3_S" {
		t.Errorf("expected model to fall back to provider default 'unsloth/Qwen3.8-27B-GGUF:IQ3_S', got %q", merged.Model)
	}
	if merged.Temperature != 0.7 {
		t.Errorf("expected temperature to fall back to 0.7, got %.2f", merged.Temperature)
	}
}

func TestClientEnvVarsSubset(t *testing.T) {
	clientVars := ClientEnvVars()
	names := make([]string, len(clientVars))
	for i, v := range clientVars {
		names[i] = v.Name
	}

	expected := []string{"NORMAN_SERVER_URL", "NORMAN_HOST", "NORMAN_PORT", "NORMAN_VERBOSE"}
	if len(clientVars) != 4 {
		t.Errorf("expected 4 client env vars, got %d", len(clientVars))
	}
	for i, exp := range expected {
		if names[i] != exp {
			t.Errorf("client var[%d]: expected %q, got %q", i, exp, names[i])
		}
	}

	// Verify server vars include all 7
	serverVars := ServerEnvVars()
	if len(serverVars) != 7 {
		t.Errorf("expected 7 server env vars, got %d", len(serverVars))
	}
}

func TestClientEnvVarHelpText(t *testing.T) {
	help := ClientEnvVarHelpText()
	if help == "" {
		t.Error("expected non-empty client help text")
	}
	if !strings.Contains(help, "NORMAN_SERVER_URL") {
		t.Error("client help should mention NORMAN_SERVER_URL")
	}
	if !strings.Contains(help, "NORMAN_VERBOSE") {
		t.Error("client help should mention NORMAN_VERBOSE")
	}
	// Client help should NOT mention directory-specific vars
	if strings.Contains(help, "STORE_DIR") || strings.Contains(help, "RAW_SOURCES_DIR") {
		t.Error("client help should NOT mention STORE_DIR or RAW_SOURCES_DIR")
	}
}

func TestServerEnvVarsAll(t *testing.T) {
	serverVars := ServerEnvVars()
	names := make([]string, len(serverVars))
	for i, v := range serverVars {
		names[i] = v.Name
	}

	expected := []string{
		"NORMAN_HOST", "NORMAN_PORT", "NORMAN_SERVER_URL",
		"NORMAN_BASE_DIR", "NORMAN_VERBOSE", "NORMAN_STORE_DIR", "NORMAN_RAW_SOURCES_DIR",
	}
	for i, exp := range expected {
		if names[i] != exp {
			t.Errorf("server var[%d]: expected %q, got %q", i, exp, names[i])
		}
	}
}
