# llamacpp Provider

llama.cpp HTTP server provider implementing `LLM` + `Embedder` interfaces with
explicit KV-cache prompt caching optimization.

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
│  POST /api/v1/chat      ← chat completions (SSE stream)│
│  POST /api/v1/embeddings ← embedding vectors            │
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
  "usage": {
    "prompt_tokens": 1200,
    "cached_tokens": 800,   // ← this tells you how many tokens were served from cache
    "completion_tokens": 150,
    "total_tokens": 1350
  }
}
```

A `cached_tokens` value > 0 means the prefix was found in KV cache. The ratio
`cached_tokens / prompt_tokens` gives your hit rate for that request.

### Tuning Guide

1. **Measure your longest repeated prefix** (system prompt + conversation history).
2. Set `CacheSize` to at least that length + 20% headroom.
3. Use `fq` type if accuracy is critical; switch to `q4_0` if memory is constrained.
4. Monitor `cached_tokens` in responses — if it's consistently 0, your cache size
   may be too small or prefixes are varying too much.

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

## Errors

Typed errors specific to this provider:
- `ErrKVCacheFull` — KV cache capacity exhausted
- `ErrPromptTooLong` — single prompt exceeds configured cache size
- `ErrCacheCorrupt` — cached KV state cannot be restored
