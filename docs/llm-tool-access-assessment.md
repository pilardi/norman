# LLM Tool Access Assessment — Agent-to-Environment Integration

> **Date:** 2026-10-21  
> **Author:** Norman agent (session assessment)  
> **Scope:** How tools are granted to the LLM for interacting with the OpenShell workspace environment.  
> **Constraint:** The agent must never execute commands on the host machine — only inside its sandbox or via interaction with the user.

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
| **Read file** (`cat`, `head`, etc.) | ❌ Not a dedicated tool — agent must write shell commands manually | Agent constructs `"ls -la /workspace"` as a JSON blob; no typed `read_file(path)` call |
| **Write file** (`echo >`, `tee`) | ❌ Same — manual shell scripting in LLM output | No structured `write_file(path, content)` API |
| **Execute command** (typed) | ⚠️ Exists as `RunCommand` but not exposed as a tool to the LLM | The loop calls it internally; the LLM doesn't invoke it directly with typed arguments |
| **Permission request / ask user** | ❌ No agent-initiated notification mechanism | User interaction is server-side push (notifier), not agent-initiated Q&A |
| **Send notification to user** | ⚠️ Partial — `Notifier.Notify()` exists but isn't wired into the LLM loop | The notifier is triggered by task completion/threshold events, not by the agent mid-turn |
| **List directory / glob** | ❌ Manual shell command in prompt text | No `list_dir(path)` tool |
| **Structured argument passing** | ⚠️ Weak — tools receive a single `"args"` object with no schema enforcement | Skills define parameters but they're all `{type: "object", properties: {args: {...}}}` — no per-tool schemas |

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

### 4.1 Missing Structured Shell Tools

The current `buildTools()` in `loop.go` only exposes **activated skills** as tools:

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

**Problem:** There is no generic `bash`, `read_file`, or `write_file` tool — only whatever skills are activated. The agent must compose shell commands as natural text in its JSON blob output.

### 4.2 No Agent-Initiated User Interaction

The `notify.Notifier` exists but is only triggered by:
- Task completion events (`pushCompletion`)
- Budget threshold events (`pushThresholdNotification`)

There is **no mechanism for the LLM to call a tool that asks the user a question** or requests permission mid-task. The interaction flow is one-directional (server → user via WebSocket), not bidirectional agent↔user.

### 4.3 Raw Sources Are Read-Only Snapshots

`RawSources.Snapshot()` records environment reads but doesn't expose a live `read_file(path)` tool. The context injection (`buildMessages`) embeds raw source content as markdown blocks — useful for "reading" but not for targeted file access during task execution.

---

## 5. Recommendations

### P0 (Must Fix Before Production)

1. **Add generic in-sandbox tools** to the LLM tool list:
   - `bash(command)` — typed string argument, executes via `RunCommand`
   - `read_file(path)` — typed string argument, reads file content
   - `write_file(path, content)` — two typed arguments
   
   These should be **always available**, not dependent on skill activation.

2. **Wire up the Notifier as an agent tool** (`ask_user`, `notify`) so the LLM can initiate bidirectional communication with the user.

### P1 (Important for MVP Completion)

3. **Define per-tool parameter schemas** instead of the generic `{args: object}` pattern. Each tool should have its own `properties` definition.

4. **Add `list_dir(path)` and `find_files(pattern)`** as read-only helpers — these are common operations that currently require manual shell scripting in the LLM output.

5. **Make MCP tools discoverable at runtime** — instead of requiring skill activation, allow the agent to call `ListTools()` on any connected MCP endpoint directly.

### P2 (Post-MVP)

6. **Add a permission request tool** (`request_permission(description, on_approve, on_deny)`) for guarding destructive actions.

7. **Consider a `file_diff(path)` tool** for comparing file states before/after edits.

---

## 6. Architecture Diagram (Current → Proposed)

```
┌─────────────────────────────────────────────────────┐
│                    User / Client                      │
│              (WebSocket, Bell, Slack)                 │
└──────────────┬──────────────────────────────────────┘
               │ ask_user() ← notify.notify()
               │
┌──────────────▼──────────────────────────────────────┐
│              Agent Loop (loop.go)                     │
│  ┌─────────────────────────────────────────────┐    │
│  │ LLM Multi-Turn Loop                         │    │
│  │  tools[] = [bash, read_file, write_file,    │    │
│  │             ask_user, notify, mcp_call]     │    │
│  └──────────────┬─────────────────────────────┘    │
│         tool calls parsed from LLM response         │
├─────────────────┼───────────────────────────────────┤
│  In-Sandbox Tools        Host-Interaction Tools      │
│  ┌──────────────────┐   ┌──────────────────────┐   │
│  │ bash(command)     │   │ ask_user(question)    │   │
│  │ read_file(path)   │   │ notify(title, body)  │   │
│  │ write_file(p,c)   │   │ request_permission() │   │
│  │ list_dir(path)    │   └──────────────────────┘   │
│  │ find_files(pat)   │                               │
│  └────────┬─────────┘                                │
│           │ RunCommand / session.Exec()              │
├───────────▼──────────────────────────────────────────┤
│         OpenShell Sandbox (per-workspace)            │
│         (isolated from host machine)                 │
└──────────────────────────────────────────────────────┘
```

---

## 7. Files to Modify for Implementation

| File | Change |
|------|--------|
| `pkg/llm/interfaces.go` | Add `ToolCall` struct with typed `Arguments` field (currently uses `json.RawMessage`) |
| `pkg/agent/loop.go` | `buildTools()` — add generic tools alongside skill tools; wire `ask_user`/`notify` to Notifier |
| `pkg/env/session.go` | Add `ReadFile(path)` and `WriteFile(path, content)` methods on Session |
| `pkg/notify/notifier.go` | Expose a method callable from the LLM loop for agent-initiated notifications |
| `pkg/mcp/client.go` | Expose `ListTools()` and `CallTool()` as discoverable tools (not just skill-bound) |

---

## 8. Summary

| Aspect | Current State | Target State |
|--------|--------------|--------------|
| Tool granularity | Skill-based, generic `{args}` blob | Typed, per-tool schemas |
| Shell access | Manual text → `RunCommand` | Structured `bash(command)` tool |
| File I/O | Read-only raw sources injection | `read_file` + `write_file` tools |
| User interaction | Server push only (notifier) | Bidirectional `ask_user`, `notify` |
| Host isolation | ✅ Enforced via sandbox | ✅ Maintained (no host commands) |
| MCP integration | Skill-bound, manual activation | Discoverable at runtime |

The core constraint — **never access the host machine** — is already well-served by the OpenShell sandbox model. The gap is in **tool exposure quality**: the LLM currently "writes its own shell" rather than calling typed tools, and has no native mechanism to interact with the user beyond task completion notifications.

---

*Assessment complete. See `requirements.md` §4 for design constraints.*
