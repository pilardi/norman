// Package llm provides a provider factory that creates LLM clients from
// workspace configuration. It decides which backend implementation to use
// based on the provider type, keeping the concrete implementations private.
package llm

import (
	"fmt"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/llm/internal/llamacpp"
)

// NewProvider creates an LLM client adapter from a resolved provider config
// and workspace configuration. It selects the appropriate backend
// implementation based on provCfg.Type.
func NewProvider(name string, provCfg *config.ProviderConfig, wsCfg *config.WorkspaceConfig) (*ClientAdapter, error) {
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
