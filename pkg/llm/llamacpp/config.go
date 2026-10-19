// Package llamacpp provides provider-specific configuration types for llama.cpp.
package llamacpp

import (
	"fmt"
)

// Config holds llama.cpp HTTP server connection and caching settings.
type Config struct {
	BaseURL string // e.g., "http://localhost:8080" or the llama.cpp server address

	// Cache optimization settings.
	CacheEnabled bool   `yaml:"cache_enabled"`     // Enable --cache-prompt on the server
	CacheSize    int    `yaml:"cache_size"`        // Max KV cache tokens (n_ctx)
	CacheType    string `yaml:"cache_type"`        // "fq" (full precision) or quantized type
	CacheHashing bool   `yaml:"cache_hashing"`     // Use content hashing for cache lookup (server-side)

	// Model selection.
	Model string `yaml:"model"` // model identifier, e.g., "llama3.1:8b"

	// Request defaults.
	Temperature  float64 `yaml:"temperature"`
	MaxTokens  int     `yaml:"max_tokens"`
	StopSequences []string `yaml:"stop_sequences"`
}

// DefaultConfig returns a Config with sensible defaults for local llama.cpp servers.
func DefaultConfig() *Config {
	return &Config{
		BaseURL:       "http://localhost:8080",
		CacheEnabled:  true,
		CacheSize:     4096, // 4k tokens — good default for repeated short prompts
		CacheType:     "fq", // full precision for accuracy
		CacheHashing:  true,
		Model:         "",   // empty = use system default
		Temperature:     0.7,
		MaxTokens:     32000,
		StopSequences: []string{},
	}
}

// Validate checks required fields and returns errors.
func (c *Config) Validate() error {
	if c.BaseURL == "" {
		return fmt.Errorf("llamacpp.config.base_url is required")
	}

	if c.CacheSize <= 0 {
		return fmt.Errorf("llamacpp.config.cache_size must be > 0, got %d", c.CacheSize)
	}

	validTypes := map[string]bool{
		"fq": true, "q4_0": true, "q8_0": true, "iq2_xs": true,
	}
	if c.CacheType != "" && !validTypes[c.CacheType] {
		return fmt.Errorf("llamacpp.config.cache_type must be one of %v, got %q",
			[]string{"fq", "q4_0", "q8_0", "iq2_xs"}, c.CacheType)
	}

	if c.Temperature < 0 || c.Temperature > 2 {
		return fmt.Errorf("llamacpp.config.temperature must be between 0 and 2, got %.2f", c.Temperature)
	}

	if c.MaxTokens < 1 {
		return fmt.Errorf("llamacpp.config.max_tokens must be >= 1, got %d", c.MaxTokens)
	}

	return nil
}

// MergeWithSystem merges a workspace-level llamacpp config into system defaults.
func (c *Config) MergeWithSystem(sys *Config) *Config {
	result := DefaultConfig()

	if sys != nil {
		result.BaseURL = sys.BaseURL
		result.CacheEnabled = sys.CacheEnabled
		result.CacheSize = sys.CacheSize
		result.CacheType = sys.CacheType
		result.CacheHashing = sys.CacheHashing
	}

	// Workspace-level overrides take precedence
	if c.BaseURL != "" {
		result.BaseURL = c.BaseURL
	}
	if !c.CacheEnabled && result.CacheEnabled {
		result.CacheEnabled = false // explicit disable at workspace level
	}
	if c.CacheSize > 0 {
		result.CacheSize = c.CacheSize
	}
	if c.CacheType != "" {
		result.CacheType = c.CacheType
	}
	if c.Model != "" {
		result.Model = c.Model
	}
	if c.Temperature > 0 {
		result.Temperature = c.Temperature
	}
	if len(c.StopSequences) > 0 {
		result.StopSequences = c.StopSequences
	}

	return result
}
