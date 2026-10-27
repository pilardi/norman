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

## Errors

Typed errors specific to this provider:
- `ErrKVCacheFull` — KV cache capacity exhausted
- `ErrPromptTooLong` — single prompt exceeds configured cache size
- `ErrCacheCorrupt` — cached KV state cannot be restored
