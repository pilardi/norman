# LLM Abstraction Proposal — Provider-Independent Interface Design

> **Date:** 2026-10-18
> **Status:** Draft — for discussion
> **Related:** `docs/llm-integration-plan.md`, `requirements.md` §4.1, `go.dev/blog/llmpowered`

---

## 1. Motivation

The current `pkg/llm/client.go` couples the agent loop to a single HTTP-based chat completion API (OpenAI-compatible). The Go blog article "Building LLM-powered applications in Go" highlights several patterns that suggest our abstraction is too narrow for production readiness:

| Pattern | Why it matters for Norman |
|---------|--------------------------|
| **Provider-agnostic interfaces** | Swap GPT → Claude → local Ollama without rewriting the agent loop |
| **Batch operations** | Efficiently embed many documents or send batched completion requests |
| **Streaming responses** | Reduce perceived latency for long answers |
| **Separate embedding service** | Distinguish `Embedder` from `LLM` — they have different performance/latency profiles |
| **Prompt management** | Template-based prompts with versioning, not just raw strings |

This proposal defines a set of interfaces that decouple the agent loop from any single provider while remaining backward-compatible with our existing `Caller` interface.

---

## 2. Proposed Interfaces

### 2.1 Core Abstractions

```go
// LLM provides text completion with optional tool-calling support.
type LLM interface {
    // Complete sends a chat completion request and returns the full response.
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)

    // Stream sends a chat completion request and yields responses incrementally.
    Stream(ctx context.Context, req CompletionRequest) (StreamReader, error)

    // BatchComplete sends multiple completion requests in a single API call (provider-dependent).
    BatchComplete(ctx context.Context, reqs []CompletionRequest) ([]*CompletionResponse, error)

    // ProviderInfo returns metadata about the underlying provider.
    ProviderInfo() ProviderMetadata
}

// Embedder provides vector embedding for text content.
type Embedder interface {
    // EmbedText returns a float32 vector for the given text.
    EmbedText(ctx context.Context, text string) ([]float32, error)

    // BatchEmbedText embeds multiple texts in one call (provider-dependent).
    BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error)
}

// CompletionRequest is the universal request shape for all providers.
type CompletionRequest struct {
    Messages  []Message
    Tools     []Tool
    Model     string        // Provider-specific model identifier
    Temperature float64
    MaxTokens   int
    StopSequences []string
    PresencePenalty  float32
    FrequencyPenalty float32
}

// CompletionResponse is the universal response shape.
type CompletionResponse struct {
    ID           string
    Model        string
    Message      Message
    FinishReason string
    Usage        Usage
    // RawJSON preserves the provider's exact JSON for debugging.
    RawJSON []byte
}

// StreamReader yields incremental chunks from a streaming response.
type StreamReader interface {
    Next() (Chunk, error)
    Close() error
}

type Chunk struct {
    Delta      string
    FinishReason string
    Usage      *Usage
}

// ProviderMetadata describes the provider's capabilities and constraints.
type ProviderMetadata struct {
    Name           string  // "openai", "anthropic", "ollama", "gemini"
    SupportsTools  bool
    SupportsStream bool
    MaxTokensPerCall int
    BatchSupport   bool
}
```

### 2.2 Rationale for `BatchComplete` / `BatchEmbedText`

The Go blog article specifically highlights batch APIs (`embModel.BatchEmbedContents`, `wvClient.Batch`) as a key efficiency pattern. Our current `Complete()` is one request per call — adding `BatchComplete` lets us:
- Send multiple tool-call results back in one API round-trip
- Embed many documents at once for the RAG pipeline
- Reduce network latency overhead for bulk operations

### 2.3 Why Separate `Embedder` from `LLM`?

| Concern | LLM | Embedder |
|---------|-----|----------|
| Latency profile | High (generates tokens one-by-one) | Low (single forward pass) |
| Cost model | Per-token or per-output | Per-input token (usually cheaper) |
| Context window | Large (context + generation) | Small (input text only) |
| Typical providers | GPT-4o, Claude, Gemini Pro | text-embedding-3-small, Cohere embed |

Separating them lets us:
- Use a fast, cheap embedding model for RAG similarity search
- Use a different (potentially larger) model for generation
- Swap each independently without affecting the other

---

## 3. Migration Path from Current `Caller` Interface

The existing `Caller` interface is preserved as a **compatibility adapter**:

```go
// Caller is the legacy MVP interface — implemented by ClientAdapter below.
type Caller interface {
    Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error)
}

// ClientAdapter wraps an LLM implementation to satisfy the legacy Caller interface.
type ClientAdapter struct {
    llm LLM
}

func (a *ClientAdapter) Complete(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*CompletionResponse, error) {
    req := CompletionRequest{
        Messages:  append([]Message{{Role: "system", Content: systemPrompt}}, messages...),
        Tools:     tools,
    }
    resp, err := a.llm.Complete(ctx, req)
    if err != nil {
        return nil, err
    }
    // Preserve existing field names for backward compatibility
    return &CompletionResponse{
        ID:           resp.ID,
        Model:        resp.Model,
        Choices:      []ResponseChoice{{Message: resp.Message, FinishReason: resp.FinishReason}},
        Usage:        resp.Usage,
    }, nil
}
```

This means **zero breaking changes** for `pkg/agent/loop.go` or `cmd/norman-server/main.go` during the transition. The `Loop` continues to accept `llm.Caller`; we simply instantiate a `ClientAdapter{llm: NewOpenAILLM(...)}` or `ClientAdapter{llm: NewOllamaLLM(...)}`.

---

## 4. Provider Implementations (Planned)

| Provider | Package | Status |
|----------|---------|--------|
| OpenAI-compatible (default) | `pkg/llm/openai.go` | ✅ Existing `Client` → refactor into `OpenAILLM` |
| Ollama (local) | `pkg/llm/ollama.go` | 🟡 Planned |
| Anthropic Claude | `pkg/llm/anthropic.go` | 🟡 Planned |
| Google Gemini | `pkg/llm/gemini.go` | 🟡 Planned |

Each provider implements `LLM` + `Embedder` (if the provider supports embeddings natively). Ollama, for example, exposes both chat and embedding endpoints via its HTTP API.

---

## 5. Configuration Changes

Add a `providers` section to `~/.config/norman/config.yaml`:

```yaml
llm:
  providers:
    - name: "default"
      type: "openai"
      endpoint: "https://api.openai.com/v1"
      model: "gpt-4o"
      api_key_ref: "${secret:openai-key}"

    - name: "local"
      type: "ollama"
      endpoint: "http://localhost:11434"
      model: "llama3.1:8b"

    - name: "embed"
      type: "openai"
      endpoint: "https://api.openai.com/v1"
      model: "text-embedding-3-small"
      api_key_ref: "${secret:openai-key}"
      is_embedder: true

  default_provider: "default"
  embedder_provider: "embed"
```

The `is_embedder` flag lets the config declare which provider handles embeddings vs. generation, enabling the separation described in §2.3.

---

## 5.1 Runtime Configuration (Implemented)

The provider selection is controlled at two levels:

### CLI Flags

| Flag | Purpose |
|------|---------|
| `-p NAME`, `--provider NAME` | Select a configured provider by name (overrides `default_provider`) |
| `-v`, `--verbose` | Enable server-side debug logging |
| `-h`, `--help` | Show usage |

### Config File (`~/.norman/config.yaml`)

```yaml
llm:
  providers:
    - name: "default"
      type: "openai"
      endpoint: "https://api.openai.com/v1"
      model: "gpt-4o"
      api_key_ref: "${secret:openai-key}"

    - name: "local"
      type: "llamacpp"
      endpoint: "http://localhost:8080"
      model: "llama3.1:8b"
      cache_size: 4096       # KV cache size in tokens
      cache_type: "fq"       # fq | q4_0 | q8_0
      cache_enabled: true

  default_provider: "default"
```

### Resolution Priority

```
CLI flag (-p) > config.yaml `default_provider` > first configured provider
```

### Wiring (main.go)

The server resolves the selected provider and instantiates the correct client:

```go
provCfg, err := cfg.System.ResolveProvider(selectedName)
switch provCfg.Type {
case "openai":
    llmClient = llm.NewClientAdapter(llm.NewClient(cfg.System))
case "llamacpp":
    llmClient = llamacpp.NewProvider(wsCfg, llmCfg)
}
```

---

## 6. Impact on Existing Components

| Component | Change | Effort |
|-----------|--------|--------|
| `pkg/llm/client.go` | Refactor `Client` → `OpenAILLM`; add `Stream()` and `BatchComplete()` | Medium |
| `pkg/agent/loop.go` | Accept `Caller` (unchanged); optionally use `LLM` directly for streaming | Low |
| `pkg/llm/prompt_builder.go` | No change — still produces `[]Message` + `string` system prompt | None |
| `cmd/norman-server/main.go` | Wire `ClientAdapter{llm: NewOpenAILLM(cfg)}` instead of `NewClient()` | Low |
| `pkg/memory/store.go` (future RAG) | Would use `Embedder.BatchEmbedText()` for document ingestion | N/A (future) |

---

## 7. Open Questions for Discussion

1. **Should `BatchComplete` be a separate interface or part of `LLM`?**
   - *Resolved:* Batch operations are deferred from `LLM`; kept only on `Embedder`. The `LLM` interface has `Complete()` and `Stream()`.

2. **Streaming: yield `Chunk` or `string`?**
   - *Resolved:* `StreamReader` yields `Chunk` (with `Delta`, `FinishReason`, `Usage`). The agent loop can discard metadata if unused.

3. **Should we add a `PromptTemplate` type?**
   - The article's Genkit example emphasizes prompt templates with variables, versioning, and testing.
   - Could be a separate `pkg/prompt/` package — or fold into `prompt_builder.go`.
   - *Tentative:* Defer to post-MVP; the current string-based approach works for MVP.

4. **Error handling strategy:**
   - Provider-specific errors (rate limit, context window exceeded) vs. generic network errors.
   - Should we define a typed error enum (`ErrRateLimit`, `ErrContextWindow`, `ErrProviderDown`)?
   - *Resolved:* Typed errors implemented in `pkg/llm/errors.go` with `Is*()` helpers. Provider-specific errors (e.g., KV cache full, prompt too long) are defined per-provider.

5. **Provider selection: how to expose config?**
   - *Resolved:* YAML `providers:` list + CLI `-p/--provider` flag + `default_provider` selector. See §5.1 for details.

6. **KV cache optimization for llama.cpp — what's the right API surface?**
   - Should caching be server-side (`--cache-prompt`) only, or also client-managed (save/restore KV state)?
   - *Tentative:* Server-side via `--cache-prompt` flag; client exposes `CacheSize`, `CacheType`, `CacheEnabled` config options.

7. **Embedding provider separation?**
   - Should embeddings use a dedicated provider (e.g., `text-embedding-3-small`) or the same LLM endpoint?
   - *Tentative:* Configurable via `is_embedder: true` flag on a provider entry.

---

## 8. Summary

This proposal introduces a **provider-agnostic `LLM` + `Embedder` interface** that:
- Preserves backward compatibility via `ClientAdapter` wrapping the existing `Caller`
- Adds batch operations and streaming support (from the Go blog article patterns)
- Separates embedding from generation (important for RAG pipeline efficiency)
- Enables zero-code swaps between providers by changing only config + one factory call

The migration is additive — no breaking changes to the agent loop or session workflow.

### What's Built (as of this revision)

| Feature | Status | Location |
|---------|--------|----------|
| `LLM` + `Embedder` interfaces | ✓ | `pkg/llm/interfaces.go` |
| Typed errors (`ErrRateLimit`, etc.) | ✓ | `pkg/llm/errors.go` |
| OpenAI-compatible client with streaming | ✓ | `pkg/llm/client.go` |
| ClientAdapter wrapper | ✓ | `pkg/llm/adapter.go` |
| Agent loop streaming integration | ✓ | `pkg/agent/loop.go` |
| Provider config in `SystemConfig` | ✓ | `pkg/config/config.go` |
| `-p/--provider` CLI flag | ✓ | `cmd/norman-server/main.go`, `cmd/norman/main.go` |
| llamacpp provider with KV cache optimization | ✓ | `pkg/llm/llamacpp/` |

### Remaining (Post-MVP)

- Anthropic Claude provider implementation
- Google Gemini provider implementation
- PromptTemplate type (§7.3, deferred)
- Embedding-specific provider configuration
- Deployment patterns (health checks, graceful shutdown, connection pooling)

---

*For discussion: see `docs/llm-integration-plan.md` for remaining MVP work items.*
