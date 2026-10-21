# LLM Tool Access Assessment — Agent-to-Environment Integration

> **Date:** 2026-10-21  
> **Author:** Norman agent (session assessment)  
> **Scope:** How tools are granted to the LLM for interacting with the OpenShell workspace environment.  
> **Constraint:** The agent must never execute commands on the host machine — only inside its sandbox or via interaction with the user.
>
> **Status Update (2026-10-21):** P0 + ask_user/notify wired on branch `feature/llm-tool-access-assessment`.  
> Generic typed tools (`bash`, `read_file`, `write_file`) and bidirectional user tools (`ask_user`, `notify`) are now always available to the LLM.

---

## 1. Current Architecture Overview

### 1.1 Entry Points

| Layer | Component | Role |
|-------|-----------|------|
| `pkg/agent/loop.go` | Agent loop | Orchestrates multi-turn LLM execution, sandbox lifecycle, skill resolution |
| `pkg/llm/interfaces.go` | LLM interface | Provider-agnostic completion + streaming with tool-calling support |
| `pkg/llm/client.go` | OpenAI-compatible client | Concrete HTTP-based provider implementation |
| `pkg/mcp/client.go` | MCP client | Remote skill tools via SSE transport |
| `pkg/skills/activate.go` | Activator | Manages active skill set, injects tool definitions into prompt |
| `pkg/env/session.go` | Session (OpenShell) | Executes commands inside the sandbox |
| `pkg/notify/notifier.go` | Notifier | Pushes results to user via WebSocket/bell/Slack |

### 1.2 Tool Access Flow (Current)

```
User Prompt → Loop.execute()
    ↓
Skill Resolution → Activator.Active() → []llm.Tool definitions
    ↓
LLM API call (streaming) with tools[] in CompletionRequest
    ↓
LLM returns tool calls (JSON blobs in content)
    ↓
Loop.ParseToolCalls() → extracts name + args
    ↓
OpenShell.RunCommand(ctx, sandboxName, args_json_string)
    ↓
Sandbox executes command → stdout/stderr returned to LLM as "tool" message
    ↓
Loop loops back for next turn (max 8 iterations)
```

### 1.3 Sandbox Isolation Boundary

The agent's tool execution is **confined to the OpenShell sandbox**:

- `RunCommand(ctx, sandboxName, command)` — executes inside the sandbox
- `session.Exec(ctx, cmd)` — runs commands within a workspace session
- Sandboxes are created per-task (`norman-task-{taskID[:7]}`) and deleted after completion
- The **host machine is never directly accessed** for agent-executed commands

This satisfies the critical constraint: *the agent only operates inside its sandbox*.

---

## 2. Current Tool Exposure Model

### 2.1 What Is Exposed to the LLM Today

| Mechanism | Description | Limitation |
|-----------|-------------|------------|
| **Skill Tools** | Activated skills are registered as `llm.Tool` entries with `{name, description, parameters}` | Only skills that have been explicitly activated; no generic shell/file tools |
| **Raw Environment Context** | `RawSources` snapshots injected as markdown blocks into the prompt | Read-only, snapshot-based — not live file I/O |
| **Shell Command (via RunCommand)** | The LLM's tool call JSON blob is passed directly to `RunCommand` as a shell script | No structured API; the agent must "write its own shell commands" in natural text/JSON |

### 2.2 What Is NOT Exposed (Gaps)

| Capability | Status | Impact |
|-----------|--------|--------|
| **Read file** (`cat`, `head`, etc.) | ✅ Implemented: `read_file(path)` with typed string arg | P0 done — replaces manual `cat` shell commands |
| **Write file** (`echo >`, `tee`) | ✅ Implemented: `write_file(path, content)` with two typed args | P0 done — replaces manual `printf >` shell commands |
| **Execute command** (typed) | ✅ Implemented: `bash(command)` with typed string arg | P0 done — structured tool call instead of raw text |
| **Permission request / ask user** | ✅ Implemented: `ask_user(question)` broadcasts via Notifier, returns confirmation | P1 done — fire-and-forget for MVP; blocking wait requires response channel |
| **Send notification to user** | ✅ Implemented: `notify(title, body)` pushes via WebSocket/bell/Slack | P1 done — typed tool call replaces manual shell echo → notifier |
| **List directory / glob** | ❌ Manual shell command in prompt text | No `list_dir(path)` tool (P1) |
| **Structured argument passing** | ⚠️ Weak for skills — generic `{args}` pattern remains | Skills still use opaque `{args: object}`; P0 tools have proper typed schemas |
| **MCP tools discoverable at runtime** | ❌ Only accessible via skill activation | No general-purpose `ListTools()` call from the LLM (P1) |

### 2.3 MCP Tools (Remote)

The `pkg/mcp/client.go` provides:
- `ListTools(ctx)` — discover available tools from an MCP server
- `CallTool(ctx, name, args)` — invoke a remote tool

This is **optional and skill-specific**, not a general-purpose tool layer. It requires the skill to expose an SSE endpoint.

---

## 3. Proposed Tool Taxonomy (What Should Be Exposed)

Based on the constraint "agent must never access host machine," tools should fall into two categories:

### 3.1 In-Sandbox Tools (Execute Inside OpenShell)

| Tool | Purpose | Type |
|------|---------|------|
| `bash(command)` | Execute arbitrary shell command in sandbox | Executable |
| `read_file(path)` | Read file contents | Read-only |
| `write_file(path, content)` | Write/overwrite a file | Mutable |
| `list_dir(path)` | List directory contents | Read-only |
| `find_files(pattern)` | Glob/search for files | Read-only |

These map to OpenShell's `RunCommand` / `session.Exec()` internally but provide typed APIs so the LLM doesn't need to "write its own shell."

### 3.2 Host-Interaction Tools (No Sandbox Execution)

| Tool | Purpose | Type |
|------|---------|------|
| `ask_user(question)` | Send a question to the user, wait for response | Interactive |
| `notify(title, body)` | Push a notification via WebSocket/bell/Slack | Event |
| `request_permission(description)` | Request approval before a destructive action | Interactive |

These do **not** execute in the sandbox — they interact with the user session directly.

### 3.3 MCP Tools (Remote Skill Endpoints)

| Tool | Purpose | Type |
|------|---------|------|
| `mcp_call(server, tool_name, args)` | Invoke a remote MCP tool | Remote |

---

## 4. Current Code Gaps vs. Proposed Model

### 4.1 Missing Structured Shell Tools (P0 ✅ Implemented)

**Before:** `buildTools()` only exposed **activated skills** with generic `{args: object}` parameters. No typed shell tools existed.

```go
func (l *Loop) buildTools() []llm.Tool {
    var tools []llm.Tool
    if l.activator == nil { return tools }
    for _, entry := range l.activator.Registry() {
        if !contains(l.activator.Active(), entry.Name) { continue }
        tools = append(tools, llm.Tool{
            Name:        entry.Name,
            Description: entry.Description,
            Parameters: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "args": map[string]interface{}{
                        "type": "object",
                        "description": "Tool-specific arguments",
                    },
                },
                "required": []string{"args"},
            },
        })
    }
    return tools
}
```

**After (P0):** Generic typed tools are now always prepended to the tool list:

```go
func (l *Loop) buildTools() []llm.Tool {
    var tools []llm.Tool
    // Always-available generic tools
    tools = append(tools, llm.Tool{
        Name: "bash", Description: "...", Parameters: {...}
    })
    tools = append(tools, llm.Tool{
        Name: "read_file", Description: "...", Parameters: {...}
    })
    tools = append(tools, llm.Tool{
        Name: "write_file", Description: "...", Parameters: {...}
    })
    // Then activated skills follow
    ...
    return tools
}
```

Each generic tool now has its own typed `properties` schema (not a generic `{args}` blob). A `dispatchToolCall()` handler routes parsed calls to the correct executor.

**Remaining gap for skills:** Activated skill tools still use the generic `{args: object}` pattern — per-tool schemas are P1.

### 4.2 No Agent-Initiated User Interaction (P1 ✅ Implemented)

The `notify.Notifier` now exposes two agent-callable methods:
- **`ask_user(question)`** — broadcasts a question to the connected user via WebSocket notification event; returns confirmation that it was sent. A full blocking variant would require a response channel from the client.
- **`notify(title, body)`** — pushes a typed notification via all configured channels (WebSocket, bell, Slack).

Both are now in `buildTools()` and routed through `dispatchToolCall()`. The interaction flow is now bidirectional: the LLM can initiate communication with the user mid-turn.

**P1 Status:** ✅ Done. The Notifier's `Push(title, body string)` method is exposed as a typed tool. A full blocking `AskUser(question) (string, error)` variant would require wiring a response channel from the WebSocket client — this is a P2 enhancement.

### 4.3 Raw Sources Are Read-Only Snapshots

`RawSources.Snapshot()` records environment reads but doesn't expose a live `read_file(path)` tool. The context injection (`buildMessages`) embeds raw source content as markdown blocks — useful for "reading" but not for targeted file access during task execution.

---

## 5. Recommendations & Implementation Status

### ✅ P0 — COMPLETED (2026-10-21, branch `feature/llm-tool-access-assessment`)

1. **Generic in-sandbox tools added** to the LLM tool list:
   - ✅ `bash(command)` — typed string argument, executes via `RunCommand`
   - ✅ `read_file(path)` — typed string argument, reads file content via `session.ReadFile()`
   - ✅ `write_file(path, content)` — two typed arguments, writes via shell `printf >`

   These are **always available**, prepended before activated skill tools. Each has its own typed `properties` schema (not a generic `{args}` blob). A `dispatchToolCall()` handler routes parsed calls to the correct executor.

2. **Notifier as agent tool** — ⚠️ Partially done: The Notifier exists but is NOT yet exposed as a callable tool in the LLM loop. This remains a P1 item (see below).

### 🔲 P1 — Next Implementation Priority (Remaining)

3. **Per-tool parameter schemas for skills** — Replace generic `{args: object}` with actual typed `properties` definitions per skill entry. This requires extending `SkillEntry` to carry schema metadata or generating it from the skill's tool definitions.

4. **Add `list_dir(path)` and `find_files(pattern)`** — Read-only helpers using shell `ls` and `find`/`glob`. These are common operations that currently require manual shell scripting in the LLM output.

5. **Make MCP tools discoverable at runtime** — Expose `ListTools()` and `CallTool()` as general-purpose tools (not just skill-bound). The agent should be able to call any registered MCP tool without prior skill activation.

### 🔲 P2 — Post-MVP

6. **`request_permission(description, on_approve, on_deny)`** — Guard destructive actions behind explicit user approval. Could be implemented as a blocking tool call that waits for user confirmation via the WebSocket push channel.

7. **`file_diff(path)`** — Compare file states before/after edits. Useful for reviewing changes or auditing edits.

---

## 6. Architecture Diagram (Current → Proposed)

### Current State (P0 implemented, P1 pending):

```
┌─────────────────────────────────────────────────────┐
│                    User / Client                      │
│              (WebSocket, Bell, Slack)                 │
└──────────────┬──────────────────────────────────────┘
               │ ask_user() ← notify.notify()  ⚠️ NOT YET WIRED
               │
┌──────────────▼──────────────────────────────────────┐
│              Agent Loop (loop.go)                     │
│  ┌─────────────────────────────────────────────┐    │
│  │ LLM Multi-Turn Loop                         │    │
│  │  tools[] = [bash, read_file, write_file,    │ ✅ P0
│  │             <activated skill tools>]        │    │
│  └──────────────┬─────────────────────────────┘    │
│         tool calls parsed from LLM response         │
├─────────────────┼───────────────────────────────────┤
│  In-Sandbox Tools (P0 ✅)                            │
│  ┌──────────────────┐                                │
│  │ bash(command)     │   ← typed string arg          │
│  │ read_file(path)   │   ← typed path arg            │
│  │ write_file(p,c)   │   ← typed path + content      │
│  └────────┬─────────┘                                │
│           │ RunCommand / session.ReadFile/WriteFile() │
├───────────▼──────────────────────────────────────────┤
│         OpenShell Sandbox (per-workspace)            │
│         (isolated from host machine) ✅               │
└──────────────────────────────────────────────────────┘

Legend: ✅ = implemented, ⚠️ = partially done, 🔲 = not yet started
```

---

## 7. Files to Modify for Implementation

| File | Change | Status |
|------|--------|--------|
| `pkg/llm/interfaces.go` | Add `ToolCall` struct with typed `Arguments` field (currently uses `json.RawMessage`) | 🔲 P2 |
| `pkg/agent/loop.go` | `buildTools()` — add generic tools + ask_user + notify; dispatch handlers for all tool types | ✅ P0+P1 done |
| `pkg/env/session.go` | Add `ReadFile(path)` and `WriteFile(path, content)` methods on Session | ✅ P0 done |
| `pkg/notify/notifier.go` | Expose `Push(title, body string)` method for typed LLM tool calls; `AskUser(question) (string, error)` blocking variant | ⚠️ Partially done (`Push` done, blocking `AskUser` needs response channel) |
| `pkg/mcp/client.go` | Expose `ListTools()` and `CallTool()` as discoverable tools (not just skill-bound) | 🔲 P1 |

---

## 8. Summary

| Aspect | Before P0 | After P0 (Current) | Target State |
|--------|-----------|-------------------|--------------|
| Tool granularity | Skill-based, generic `{args}` blob | ✅ Generic tools have typed schemas; skills still generic | Typed per-tool schemas for all tools |
| Shell access | Manual text → `RunCommand` | ✅ Structured `bash(command)` tool | Same (P0) |
| File I/O | Read-only raw sources injection | ✅ `read_file` + `write_file` tools | Same (P0) |
| User interaction | Server push only (notifier) | ✅ `ask_user` + `notify` wired as LLM tools; blocking variant pending | Bidirectional `ask_user` (blocking), `notify` |
| Host isolation | ✅ Enforced via sandbox | ✅ Maintained (no host commands) | Same (P0) |
| MCP integration | Skill-bound, manual activation | 🔲 No runtime discovery | Discoverable at runtime |

The core constraint — **never access the host machine** — is already well-served by the OpenShell sandbox model. The gap is in **tool exposure quality**: P0 has resolved the shell/file I/O problem (typed tools), but bidirectional user interaction and MCP discoverability remain as P1 priorities.

---

*Assessment complete. See `requirements.md` §4 for design constraints.*
