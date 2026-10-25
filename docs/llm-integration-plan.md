# LLM Integration Plan — Provider-Agnostic Abstraction & Remaining Work
> **Date:** 2026-10-19 (merged)
> **Status:** Draft — actionable items tracked for MVP completion
> **Related:** `requirements.md` §4.1, `go.dev/blog/llmpowered`

---

## 0. Design Rationale & Interfaces (from LLM Abstraction Proposal)

### 0.1 Motivation

The current `pkg/llm/client.go` couples the agent loop to a single HTTP-based chat completion API (OpenAI-compatible). The Go blog article "Building LLM-powered applications in Go" highlights several patterns that suggest our abstraction is too narrow for production readiness:

| Pattern | Why it matters for Norman |
|---------|--------------------------|
| **Provider-agnostic interfaces** | Swap GPT → Claude → local Ollama without rewriting the agent loop |
| **Batch operations** | Efficiently embed many documents or send batched completion requests |
| **Streaming responses** | Reduce perceived latency for long answers |
| **Separate embedding service** | Distinguish `Embedder` from `LLM` — they have different performance/latency profiles |
| **Prompt management** | Template-based prompts with versioning, not just raw strings |

### 0.2 Proposed Interfaces (Implemented)

```go
// LLM provides text completion with optional tool-calling support.
type LLM interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
    Stream(ctx context.Context, req CompletionRequest) (StreamReader, error)
    ProviderInfo() ProviderMetadata
}

// Embedder provides vector embedding for text content.
type Embedder interface {
    EmbedText(ctx context.Context, text string) ([]float32, error)
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

### 0.3 Migration Path from Current `Caller` Interface

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
    return &CompletionResponse{
        ID:           resp.ID,
        Model:        resp.Model,
        Choices:      []ResponseChoice{{Message: resp.Message, FinishReason: resp.FinishReason}},
        Usage:        resp.Usage,
    }, nil
}
```

This means **zero breaking changes** for `pkg/agent/loop.go` or `cmd/norman-server/main.go` during the transition.

### 0.4 Provider Implementations (Planned)

| Provider | Package | Status |
|----------|---------|--------|
| OpenAI-compatible (default) | `pkg/llm/openai.go` | ✅ Implemented |
| Ollama (local) | `pkg/llm/ollama.go` | 🟡 Planned |
| Anthropic Claude | `pkg/llm/anthropic.go` | 🟡 Planned |
| Google Gemini | `pkg/llm/gemini.go` | 🟡 Planned |

### 0.5 Configuration Changes

Add a `llm_providers` section to `~/.config/norman/config.yaml`:

```yaml
llm_providers:
  - name: "default"
    type: "openai"
    endpoint: "https://api.openai.com/v1"
    model: "gpt-4o"
    api_key_ref: "${secret:openai-key}"
    temperature: 0.7
    max_tokens: 8192

  - name: "local"
    type: "ollama"
    endpoint: "http://localhost:11434"
    model: "llama3.1:8b"
    temperature: 0.5
    max_tokens: 4096

  - name: "embed"
    type: "openai"
    endpoint: "https://api.openai.com/v1"
    model: "text-embedding-3-small"
    api_key_ref: "${secret:openai-key}"
    is_embedder: true
```

### 0.6 Runtime Configuration (Implemented)

The provider selection is controlled at two levels:

| Flag | Purpose |
|------|---------|
| `-p NAME`, `--provider NAME` | Select a configured provider by name (overrides positional default) |
| `-v`, `--verbose` | Enable server-side debug logging |

### Resolution Priority

```
CLI flag (-p) > first provider in list (positional default)
```

> **Note:** Temperature and max_tokens are now per-provider (not system-level). The workspace-level `model`, `max_tokens`, and `temperature` fields override the *provider's* defaults when set to non-zero/non-empty values.

---

## 1. Current State Summary

| Component | Status | Notes |
|-----------|--------|-------|
| `pkg/llm/client.go` | ✅ Complete | OpenAI-compatible API client with `Complete()` signature + `Caller` interface |
| `pkg/llm/prompt_builder.go` | ✅ Complete | `BuildContext()` assembles messages from raw sources + skills + session history |
| `pkg/agent/loop.go` | ✅ Fixed (2026-10-17) | `WithLLMClient` accepts `llm.Caller`; `executeWithLLM` passes tools; `buildTools()` implemented |
| `cmd/norman-server/main.go` | ✅ Wired | LLM client injected at startup via `loop.WithLLMClient(llm.NewClient(cfg.System))` |
| `pkg/agent/loop_test.go` | ✅ Extended | 3 integration tests for multi-turn, max-turns, shell-only fallback |
| Build (`go build ./...`) | ✅ Clean | Compiles without errors; all tests pass |

---

## 2. Remaining Compile / Wiring Issues

### 2.1 Wire LLM Client in `main.go`

**Problem:** `main.go` does not call `loop.WithLLMClient(llm.NewClient(cfg.System))` at startup, so the loop runs in shell-only mode even when an LLM endpoint is configured.

**Fix:**
```go
// In main.go, after creating the Loop:
if cfg.System.OpenShell.Address != "" {
    loop = loop.WithLLMClient(llm.NewClient(cfg.System))
}
```

This is a one-line addition inside the `main()` function where the `Loop` struct is constructed.

### 2.2 Tool Call Format Decision (Design)

The current `ParseToolCalls()` in `pkg/llm/client.go` uses a naive JSON-in-content heuristic:

```go
func ParseToolCalls(msg Message) []ToolCall {
    // For MVP, we parse the content as JSON if it looks like a tool call
    var calls []ToolCall
    if msg.Role == "assistant" && len(msg.Content) > 0 {
        var raw map[string]interface{}
        if err := json.Unmarshal([]byte(msg.Content), &raw); err == nil {
            if name, ok := raw["name"].(string); calls = append(calls, ...)
        }
    }
    return calls
}
```

**Open questions for production:**

| Question | Current Approach | Alternative |
|----------|-----------------|-------------|
| Tool call format | JSON blob in `content` field | Structured `tool_calls` array per OpenAI spec |
| Shell args | Pass raw JSON string to `RunCommand` | Parse JSON → format as shell arguments (`--flag val`) |
| Multi-tool batch | Execute each tool sequentially, append results | Batch into single command where possible |

**Recommendation for MVP:** Keep the current JSON-in-content approach but add a `tool_calls` field to `CompletionResponse.Choices.Message` for proper OpenAI-compatible parsing. The shell args format decision should be deferred until integration testing reveals actual LLM output formats.

---

## 3. Integration Testing Required

### 3.1 SendMessage Multi-Turn Response Test

**Scope:** Verify that `Loop.SendMessage()` correctly invokes the LLM multi-turn loop and returns a structured `AgentResponse` with text + optional tool calls.

**Test plan:**
```go
func TestSendMessageMultiTurn(t *testing.T) {
    // Setup: FakeOpenShellClient with known outputs per command
    fake := NewFakeOpenShellClient()
    
    // Inject LLM client with mock responses:
    // Turn 1: tool call (e.g., "ls")
    // Turn 2: final text response
    
    resp, err := loop.SendMessage(ctx, "session-1", "List files in /tmp")
    
    assert.Equal(t, protocol.TaskStatusCompleted, resp.Status)
    assert.Contains(t, resp.Response, "expected output from ls")
}
```

**Coverage matrix:**

| Scenario | Expected Behavior |
|----------|-------------------|
| LLM returns text only (no tool calls) | Single-turn response, `task_created=false` |
| LLM returns tool call + text | Multi-turn loop, tools executed in sandbox, final text returned |
| LLM returns multiple tool calls | Sequential execution, all results appended as `tool` messages |
| LLM API error at turn N | Task status = Failed, error message propagated |
| Max turns (8) reached without completion | Status = Failed with "max turns" message |

### 3.2 Shell-Only Fallback Path

Verify that when `llmClient` is `nil`, the legacy path still works:
- Raw sources injected into prompt
- Single `RunCommand` call executes the full prompt as a shell script
- Exit code 0 → `TaskStatusCompleted`, non-zero → `TaskStatusFailed`

---

## 4. Architecture Decisions & Trade-offs

### 4.1 Context Budget at 75%/90%

The current implementation (`checkAndCompress`) triggers:
- **Warn threshold (75%):** Archive full transcript, compress aggressively
- **Critical threshold (90%):** Same but with forced summarization

**Open question:** Should the LLM loop itself be aware of remaining budget and adapt its output length? Current approach is reactive (compress after budget breach). A proactive approach would pass `remaining_tokens` into the system prompt so the LLM knows to be concise.

### 4.2 Tool Registry Integration

Currently `executeWithLLM` passes `nil` for the tools argument:
```go
resp, err := l.llmClient.Complete(ctx, systemPrompt, messages, nil)
```

**Planned extension:** Dynamically resolve active skills into `llm.Tool` entries and pass them to `Complete()`. This requires:
1. Exposing `Activator.Registry()` as a tool catalog (name + description + parameter schema)
2. Building `[]llm.Tool` from the registry before each `Complete()` call
3. Mapping returned tool call names back to skill activations

### 4.3 Echo HTTP + WebSocket Push

The `WSHubPublisher` interface already broadcasts progress events (`TaskProgress`, `TaskComplete`, `TaskFailed`) during execution. The "echo" mechanism (HTTP response body carrying the final text) is handled by:
```go
resp := &AgentResponse{
    SessionID: sessionID,
    Response:  string(task.Output),
}
```

**Remaining:** Ensure WebSocket push events are also emitted for LLM-specific steps (turn boundaries, tool call results). Currently only `Step: intPtr(6 + turn)` is broadcast — this should include tool call detail.

---

## 5. File-Based JSON Persistence

The current `memory.Store` uses file-based JSON for task state and session history. The LLM integration should:

- **Persist conversation turns** after each multi-turn loop iteration (not just at completion) so a crashed process can resume from the last turn.
- **Archive raw tool call results** alongside text responses for replay/debugging.
- **Preserve plan content** during compression (already implemented via `SavePlan`/`LoadPlan`).

**Recommendation:** Add a `persistTurn()` method to `memory.Store` called at the end of each LLM turn, not just on task completion.

---

## 6. OpenShell Sandbox Lifecycle Notes

The current flow is:
1. `CreateSandbox(ctx, workspace, name)` → returns sandbox ID
2. `WaitReady(ctx, workspace, name, timeout)` → blocks until sandbox is ready
3. `GetSandbox(ctx, provider, name)` → sanity check before execution
4. `RunCommand(ctx, sandboxName, command)` → executes tool call
5. `DeleteSandbox(ctx, workspace, name)` → cleanup (async)

**Known edge case:** The gateway may auto-delete idle sandboxes between `WaitReady` and `RunCommand`. The current code handles this with a re-fetch sanity check (`GetSandbox`). This should be extended to handle the case where the sandbox is deleted *during* execution (e.g., for very long-running tool calls).

**Recommendation:** Add a `KeepAlive()` call or heartbeat between tool executions in multi-turn loops to prevent idle-timeout deletion.

---

## 7. Action Items Checklist

| # | Item | Priority | Status | Notes |
|---|------|----------|--------|-------|
| 1 | Wire `llm.NewClient(cfg.System)` into `main.go` | **High** | ✅ Done | Committed 2026-10-17 |
| 2 | Add `TestSendMessageMultiTurn` integration test | **High** | ✅ Done | 3 tests: tool-call, max-turns, shell-only fallback |
| 3 | Extend `ParseToolCalls` to support OpenAI `tool_calls` array format | Medium | ✅ Done | Dual-strategy: structured + legacy JSON-in-content |
| 4 | Pass active skills as `[]llm.Tool` to `Complete()` (remove `nil`) | Medium | ✅ Done | `buildTools()` converts activated skills to tool definitions |
| 5 | Add `persistTurn()` to memory store for LLM conversation turns | Low | ✅ Done | `PersistTurn` + `PersistToolCallResult` in `memory.Store`; called at each turn end in `executeWithLLM` |
| 6 | Emit WebSocket events for tool call results in multi-turn loop | Low | ✅ Done | Enhanced `BroadcastSession` calls include tool name, args, and result status |
| 7 | Add sandbox keepalive heartbeat between tool executions | Low | ✅ Done | Added `KeepAlive()` call between tool executions to prevent idle-timeout deletion |

---

## 8. What's Built (as of this merge)

| Feature | Status | Location |
|---------|--------|----------|
| `LLM` + `Embedder` interfaces | ✓ | `pkg/llm/interfaces.go` |
| Typed errors (`ErrRateLimit`, etc.) | ✓ | `pkg/llm/errors.go` |
| OpenAI-compatible client with streaming | ✓ | `pkg/llm/client.go` |
| ClientAdapter wrapper | ✓ | `pkg/llm/adapter.go` |
| Agent loop streaming integration | ✓ | `pkg/agent/loop.go` |
| Per-provider config (model, temp, max_tokens) | ✓ | `pkg/config/config.go`, `config_defaults.yaml` |
| `-p/--provider` CLI flag | ✓ | `cmd/norman-server/main.go`, `cmd/norman/main.go` |
| llamacpp provider with KV cache optimization | ✓ | `pkg/llm/llamacpp/` |

### Remaining (Post-MVP)

- Anthropic Claude provider implementation
- Google Gemini provider implementation
- PromptTemplate type (§7.3, deferred)
- Embedding-specific provider configuration
- Deployment patterns (health checks, graceful shutdown, connection pooling)

---

## 9. References

- **OpenAI Chat Completions API:** https://platform.openai.com/docs/api-reference/chat/create
- **NVIDIA OpenShell SDK (Go):** `github.com/NVIDIA/OpenShell/sdk/go/openshell/v1`
- **Norman requirements spec:** `requirements.md` (project root)
- **Etymology note:** For background context on Sicily: https://en.wikipedia.org/wiki/History_of_Sicily

---

*Last updated: 2026-10-19. Items 1–7 all completed. See git history for commit hashes.*
