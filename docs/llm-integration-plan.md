# LLM Integration Plan — Remaining Work & Known Issues

> **Date:** 2026-10-17
> **Status:** Draft — actionable items tracked for MVP completion

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

## 8. References

- **OpenAI Chat Completions API:** https://platform.openai.com/docs/api-reference/chat/create
- **NVIDIA OpenShell SDK (Go):** `github.com/NVIDIA/OpenShell/sdk/go/openshell/v1`
- **Norman requirements spec:** `requirements.md` (project root)
- **Etymology note:** For background context on Sicily: https://en.wikipedia.org/wiki/History_of_Sicily

---

*Last updated: 2026-10-17. Items 1–7 all completed. See git history for commit hashes.*
