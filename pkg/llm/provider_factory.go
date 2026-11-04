// Package llm provides a provider factory that creates LLM clients from
// workspace configuration. It decides which backend implementation to use
// based on the provider type, keeping the concrete implementations private.
package llm

import (
	"fmt"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/llm/internal/llamacpp"
)

// NewProviderFromConfig creates an LLM client adapter from system configuration.
// It resolves the provider name using this priority: CLI-selected > default_provider > first configured provider.
// Returns nil if no providers are configured (no-op client).
// The resolved provider name is returned for diagnostics; empty string means no provider.
func NewProviderFromConfig(sysCfg *config.SystemConfig, selectedProvider string) (*ClientAdapter, string, error) {
	// Resolve selected provider: CLI flag > configured default_provider > first in list.
	var providerName string
	if selectedProvider != "" {
		providerName = selectedProvider
	} else if sysCfg.DefaultProvider != "" {
		providerName = sysCfg.DefaultProvider
	} else if len(sysCfg.LlmProviders) > 0 {
		providerName = sysCfg.LlmProviders[0].Name
	}

	if providerName == "" {
		return nil, "", nil // no providers configured
	}

	provCfg, err := sysCfg.ResolveProvider(providerName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve provider %q: %w", providerName, err)
	}

	client, err := newProvider(providerName, provCfg, config.DefaultWorkspaceConfig())
	return client, providerName, err
}

// GetProviderByName creates an LLM client adapter for a specific provider by name.
// This is used internally by the agent loop to resolve and cache providers per task.
func GetProviderByName(sysCfg *config.SystemConfig, name string) (*ClientAdapter, error) {
	provCfg, err := sysCfg.ResolveProvider(name)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve provider %q: %w", name, err)
	}
	return newProvider(name, provCfg, config.DefaultWorkspaceConfig())
}

// newProvider creates an LLM client adapter from a resolved provider config
// and workspace configuration. It selects the appropriate backend
// implementation based on provCfg.Type.
func newProvider(name string, provCfg *config.ProviderConfig, wsCfg *config.WorkspaceConfig) (*ClientAdapter, error) {
	switch provCfg.Type {
	case "openai":
		return NewClientAdapter(NewClientWithProvider(provCfg)), nil

	case "llamacpp":
		return newLlamaCPPProvider(name, provCfg, wsCfg)

	default:
		return nil, fmt.Errorf("unsupported provider type %q", provCfg.Type)
	}
}

// --- OpenAI-compatible provider (already handled by NewClientAdapter + Client) ---

// --- LlamaCPP provider (internal implementation) ---

func newLlamaCPPProvider(name string, provCfg *config.ProviderConfig, wsCfg *config.WorkspaceConfig) (*ClientAdapter, error) {
	// Build type-specific config from Extra map.
	llmCfg := llamacpp.DefaultConfig()
	if v, ok := provCfg.Extra["cache_size"].(float64); ok {
		llmCfg.CacheSize = int(v)
	}
	if v, ok := provCfg.Extra["cache_type"].(string); ok {
		llmCfg.CacheType = v
	}
	if v, ok := provCfg.Extra["cache_enabled"].(bool); ok {
		llmCfg.CacheEnabled = v
	}

	// Use provider-level defaults for model/temp/tokens.
	wsCfgCopy := *wsCfg
	wsCfgCopy.Model = provCfg.Model
	if provCfg.Temperature > 0 {
		wsCfgCopy.Temperature = provCfg.Temperature
	}
	if provCfg.MaxTokens > 0 {
		wsCfgCopy.MaxTokens = provCfg.MaxTokens
	}

	// Apply embedding endpoint configuration.
	var opts []llamacpp.Option
	switch provCfg.EmbeddingEndpoint {
	case "/embeddings":
		opts = append(opts, llamacpp.WithNativeEmbeddings())
	case "/api/v1/embeddings":
		opts = append(opts, llamacpp.WithOpenAICompatibleEmbeddings())
	}

	return NewClientAdapter(llamacpp.NewProvider(name, &wsCfgCopy, llmCfg, opts...)), nil
}
