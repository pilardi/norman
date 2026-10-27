# llamacpp Provider

llama.cpp HTTP server provider implementing `LLM` + `Embedder` interfaces with
explicit KV-cache prompt caching optimization. Uses llama.cpp's **native `/completion`**
endpoint (not the OpenAI-compatible `/v1/chat/completions` wrapper).

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Agent Loop (pkg/agent/loop.go)                         │
│  → calls Provider.Complete() or Provider.Stream()       │
│  → also calls Provider.EmbedText() for RAG              │
└──────────────────────┬──────────────────────────────────┘
                       │ HTTP REST API
                       ▼
┌─────────────────────────────────────────────────────────┐
│  llama.cpp HTTP Server                                  │
│                                                         │
│  POST /completion       ← native chat completions (SSE) │
│  POST /api/v1/embeddings ← OpenAI-compatible embeddings │
│  POST /embeddings     ← native embeddings (token-level) │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │ KV Cache (persistent across requests)           │   │
│  │ Hash → Saved KV State mapping                   │   │
│  │ ─────────────────────────────────────────────── │   │
│  │ Type: fq | q4_0 | q8_0                          │   │
│  │ Size: N tokens (configurable)                   │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

## API Format

The provider uses llama.cpp's **native `/completion`** endpoint rather than the
OpenAI-compatible `/v1/chat/completions` wrapper. This means:

- **Request**: `{"prompt":"...","n_predict":N,"model":"...","stream":true}`
  (single prompt string + n_predict, not a messages array)
- **Response** (native): `{"content":"...","stop_reason":"..."}`
- **SSE fallback**: If the server returns OpenAI-compatible format (`choices[].message.content`),
  the parser falls back to that format automatically.

## Prompt Caching Strategy

llama.cpp's prompt caching works by persisting KV (key-value) pairs for each
transformer layer when a prefix is first seen. On subsequent requests with the
same prefix, the server restores cached KV entries instead of reprocessing tokens.

### How to Optimize

| Parameter | Effect | Recommendation |
|-----------|--------|----------------|
| `--cache-prompt` (server flag) | Enables KV cache persistence across requests | **Always enable** for repeated prompts |
| `--ctx-size N` / `CacheSize` | Max tokens stored in KV cache | Set to ≥ longest repeated prefix length |
| `--cache-type-fq` / `q4_0` / `q8_0` | Precision vs memory tradeoff | `fq` for accuracy; `q4_0` for speed/memory constraints |
| `--cache-hash` (server flag) | Content-based hash lookup | Enable for dynamic prefix detection |

### Monitoring Cache Effectiveness

After each completion, check the response:
```json
{
  "content": "...",
  "stop_reason": "eos_token"
}
```

> **Note:** The native `/completion` endpoint does not include token usage stats
> in the response body. To monitor cache hit rates, use the `--log-disable` and
> `--log-raw` server flags or check the server's stderr output for KV-cache
> statistics (e.g., `cached_tokens: 800`).

### Tuning Guide

1. **Measure your longest repeated prefix** (system prompt + conversation history).
2. Set `CacheSize` to at least that length + 20% headroom.
3. Use `fq` type if accuracy is critical; switch to `q4_0` if memory is constrained.
4. Monitor KV-cache stats in server logs — if cache hits are consistently 0, your
   cache size may be too small or prefixes are varying too much.

## Usage

```go
import "github.com/norman/norman/pkg/llm/llamacpp"

// Create provider with default caching strategy
p := llamacpp.NewProvider(cfg,
    llamacpp.WithCacheSize(8192),   // 8k tokens cache
    llamacpp.WithType("q4_0"),      // 4-bit quantized for memory efficiency
)

// Streaming with cache-aware requests
stream, err := p.Stream(ctx, llm.CompletionRequest{...})
```

## Server Startup Flags

To enable caching on the llama.cpp server:
```bash
llama-server --model path/to/model.gguf \
    --port 8080 \
    --cache-prompt \
    --ctx-size 4096 \
    --cache-type-fq
```

## Embedding Endpoints

The provider supports **two embedding endpoints** with automatic fallback:

| Endpoint | Route | Pooling Support | Response Shape |
|----------|-------|----------------|----------------|
| OpenAI-compatible | `/api/v1/embeddings` | Requires pooling ≠ `none` | `{"data":[...],"usage":{...}}` |
| Native llama.cpp | `/embeddings` | All modes including `--pooling none` | `{"embedding":[...],"tokens":[...],"pooling":"..."}` |

### Auto-Detection (Default)

By default, `EmbedText()` tries `/api/v1/embeddings` first and falls back to `/embeddings`
if the first returns a 404 or unexpected response shape. This covers most deployment scenarios.

### Explicit Native Embeddings

When you need token-level embeddings or custom pooling modes:

```go
p := llamacpp.NewProvider(cfg,
    llamacpp.WithNativeEmbeddings(), // forces /embeddings endpoint
)
```

### Explicit OpenAI-Compatible Embeddings

When you want guaranteed OpenAI-compatible response format:

```go
p := llamacpp.NewProvider(cfg,
    llamacpp.WithOpenAICompatibleEmbeddings(), // forces /api/v1/embeddings
)
```

### When to Use Each

| Scenario | Recommended Endpoint |
|----------|---------------------|
| RAG / semantic search (sentence-level vectors) | `/api/v1/embeddings` (auto-detect) |
| Token-level similarity analysis | `/embeddings` (`WithNativeEmbeddings()`) |
| Custom pooling (e.g., `--pooling none`) | `/embeddings` (`WithNativeEmbeddings()`) |
| Drop-in OpenAI SDK replacement | `/api/v1/embeddings` (`WithOpenAICompatibleEmbeddings()`) |

### Native `/embeddings` Response Shape

When using the native endpoint, the server returns:
```json
{
  "embedding": [0.0234, -0.1567, ...],
  "tokens": [1, 2, 3, 4, 5],
  "pooling": "none"
}
```

The `pooling` field indicates which pooling mode the server is configured with (`cls`, `mean`, `last`, or `none`).

## YAML Configuration

You can configure the embedding endpoint directly in your `config.yaml`:

```yaml
llm_providers:
  - name: "local-llamacpp"
    type: "llamacpp"
    endpoint: "http://localhost:8288"
    model: "unsloth/Qwen3.8-27B-GGUF:IQ3_S"
    embedding_endpoint: "/embeddings"  # forces native /embeddings
```

Valid values for `embedding_endpoint`:
| Value | Effect |
|-------|--------|
| `"/embeddings"` | Forces native llama.cpp `/embeddings` endpoint |
| `"/api/v1/embeddings"` | Forces OpenAI-compatible `/api/v1/embeddings` endpoint |
| (empty / omitted) | Auto-detect: tries `/api/v1/embeddings` first, falls back to `/embeddings` |

> **Note:** The Go options `WithNativeEmbeddings()` and `WithOpenAICompatibleEmbeddings()` are equivalent to setting this field in YAML. Use the YAML field for persistent configuration; use the Go options when building providers programmatically.

## Errors

Typed errors specific to this provider:
- `ErrKVCacheFull` — KV cache capacity exhausted
- `ErrPromptTooLong` — single prompt exceeds configured cache size
- `ErrCacheCorrupt` — cached KV state cannot be restored
