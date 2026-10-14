# Step 8 — MCP Integration + Task Scheduling + Notifications

> **Status:** ✅ COMPLETE (all sub-steps implemented and merged to main).

---

## Problem Statement

Skills currently declare tools as static strings in their SKILL.md metadata. We need:
1. **MCP connections** per skill → dynamic tool discovery at runtime via the MCP protocol.
2. **Task scheduling** → recurring tasks (cron expressions) registered via CLI, executed by OpenShell's scheduler, results pushed back to connected clients.
3. **Notifications** → real-time delivery of task results via WebSocket + terminal bell + Slack webhook (MVP).

---

## Current State

- `pkg/mcp/` — empty stub package (ready for implementation).
- `pkg/scheduler/` — empty stub package (ready for implementation).
- `pkg/notify/` — empty stub package (ready for implementation).
- `cmd/norman/main.go` — `tasks` command prints "not yet connected" placeholder.
- `pkg/skills/discover.go` — `SkillEntry` has `Tools []string` but no MCP connection metadata.
- `pkg/skills/activate.go` — injects tools as prompt strings; no MCP client lifecycle.
- OpenShell Go SDK (`github.com/NVIDIA/OpenShell/sdk/go`) already provides sandbox lifecycle + command execution; will be extended for cron scheduling.

---

## Proposed Architecture

### 1. MCP Integration (`pkg/mcp/`)

| File | Purpose |
|------|---------|
| `client.go` | MCP client wrapper — manages connection lifecycle (connect, disconnect, list tools), caches tool definitions. |
| `registry.go` | Maps skill names → MCP clients; handles activation/deactivation of MCP connections. |
| `init.go` | Package init (keep as stub). |

**Design:**
- Each skill declares an `mcp:` section in SKILL.md frontmatter:
  ```yaml
  ---
  name: "weather"
  mcp:
    endpoint: "http://localhost:3001/mcp"
    capabilities: ["tools", "prompts"]
  ---
  ```
- `MCPClient` struct wraps the go-sdk client, provides `ListTools() ([]ToolDef, error)` and `CallTool(name string, args map[string]interface{}) (string, error)`.
- On skill activation → connect to MCP endpoint; on deactivation → disconnect.
- Tool definitions are cached and injected into agent context alongside static tools from SKILL.md.

**Integration with `pkg/skills/activate.go`:**
- `SkillEntry` extended: add `MCPClient *mcp.Client` field (nil when not active).
- `Activate()` now also calls `client.ListTools()` to discover dynamic tools at activation time.
- `Deactivate()` closes the MCP connection.

### 2. Task Scheduling (`pkg/scheduler/`)

| File | Purpose |
|------|---------|
| `scheduler.go` | Cron expression parser + task registration + execution dispatch. |
| `task.go` | `TaskEntry` struct, CLI command parsing for `norman tasks add --schedule "*/5 * * * *" health-check`. |
| `init.go` | Package init (keep as stub). |

**Design:**
- Uses `github.com/robfig/cron/v3` (or standard library if preferred — but robfig is more feature-rich with timezone support).
- `Scheduler` manages a map of registered tasks keyed by ID.
- Each task: `{ID, Name, Schedule (string cron expr), Command (shell command or MCP tool call), OutputHandler (callback)}`.
- Runs in background goroutine; on tick → executes command via OpenShell `Exec()` and pushes result via `WSHub.Push()`.
- CLI commands:
  - `norman tasks add --schedule "*/5 * * * *" health-check` — register recurring task
  - `norman tasks list` — show registered tasks + next run time
  - `norman tasks remove <id>` — unregister task
  - `norman tasks run <id>` — trigger immediately

**Integration with OpenShell:**
- Uses `OpenShellClient.Exec().Run()` to execute the scheduled command in the workspace sandbox.
- Results pushed via WebSocket `task_progress` / `task_complete` events (already defined in `pkg/protocol/types.go`).

### 3. Notifications (`pkg/notify/`)

| File | Purpose |
|------|---------|
| `notifier.go` | Notification delivery — WebSocket push + terminal bell + Slack webhook. |
| `init.go` | Package init (keep as stub). |

**Design:**
- `Notifier` struct with pluggable handlers:
  - `WSHandler` — pushes to connected WebSocket clients (already wired via `WSHub`).
  - `BellHandler` — prints `\a` (bell character) + brief text to terminal.
  - `SlackHandler` — sends webhook POST to configured Slack URL (MVP, no OAuth).
- Rate limiting: max 10 notifications/minute across all tasks (token bucket algorithm).
- Config in `~/.config/norman/config.yaml`:
  ```yaml
  notify:
    slack_webhook: ""  # optional
    bell_enabled: true
    rate_limit_per_minute: 10
  ```

**Integration with agent loop:**
- `Loop` injects `*notify.Notifier` → on task completion, calls `notifier.Notify(taskID, result)`.
- WebSocket event types already defined: `TaskProgress`, `TaskComplete`, `TaskFailed`, `SessionCompressed`. Add `Notification` type for general notifications.

---

## Data Model

```go
// pkg/mcp/client.go
type ToolDef struct {
    Name        string            `json:"name"`
    Description string            `json:"description"`
    Parameters  map[string]any    `json:"parameters,omitempty"`
}

type MCPClient struct {
    Endpoint string
    Client   *mcp.Client // from go-sdk
    Tools    []ToolDef
    Active   bool
}

// pkg/scheduler/task.go
type TaskEntry struct {
    ID          string    // unique task identifier
    Name        string    // human-readable name
    Schedule    string    // cron expression (5 or 6 field)
    Command     string    // shell command or "mcp:<tool_name>"
    OutputHandler func(result string, err error) // callback for results
    NextRun     time.Time // computed by scheduler on tick
}

// pkg/notify/notifier.go
type Notification struct {
    TaskID   string
    Title    string
    Body     string
    Severity string // "info", "warn", "error"
    Timestamp time.Time
}

type Notifier struct {
    WS       *WSHubPublisher  // reference to existing hub
    Bell     bool
    SlackURL string           // optional webhook URL
    RateLimit int             // per-minute cap
}
```

---

## Integration Points

| Component | Change |
|-----------|--------|
| `pkg/skills/discover.go` | Extend `SkillEntry` with `MCPEndpoint`, `MCPCapabilities []string`. Parse `mcp:` section from SKILL.md frontmatter. |
| `pkg/skills/activate.go` | On `Activate()`: connect MCP client, discover tools; on `Deactivate()`: close connection. |
| `cmd/norman/main.go` | Add `tasks add`, `tasks list`, `tasks remove`, `tasks run` subcommands with cron parsing. |
| `pkg/agent/loop.go` | Inject `*scheduler.Scheduler` and `*notify.Notifier`; call notifier on task completion. |
| `cmd/norman-server/main.go` | Add `POST /api/tasks` (register), `GET /api/tasks` (list), `DELETE /api/tasks/:id` endpoints. Extend WSHub to push notifications. |
| `pkg/config/config.go` | Add `NotifyConfig` struct with Slack URL, bell flag, rate limit. |

---

## Implementation Steps (sub-steps)

| Sub-step | Description | Risk | Status |
|----------|-------------|------|--------|
| **8a** | Extend `SkillEntry` + SKILL.md parser to read `mcp:` section; implement `pkg/mcp/client.go` with go-sdk wrapper. Add unit tests. | Low — MCP client is a thin wrapper around go-sdk | ✅ COMPLETE |
| **8b** | Implement `pkg/scheduler/task.go` + `scheduler.go` — cron parsing, task registration, background tick loop using OpenShell `Exec()`. Add CLI subcommands (`tasks add/list/remove/run`). | Medium — requires integration with agent loop for result pushing | ✅ COMPLETE |
| **8c** | Implement `pkg/notify/notifier.go` — WS push + terminal bell + Slack webhook; rate limiting. Wire into `Loop.onTaskComplete()`. | Low — notification is a thin delivery layer | ✅ COMPLETE |
| **8d** | Add REST endpoints (`POST /api/tasks`, `GET /api/tasks`, `DELETE /api/tasks/:id`) + extend WSHub with notification event type. Add config schema for notify settings. | Low — thin API wrapper over scheduler/notifier | ✅ COMPLETE |

---

## Acceptance Criteria

- [ ] A skill with `mcp:` section in SKILL.md → tools appear dynamically when activated (verified via `norman skills list --verbose`).
- [ ] `norman tasks add --schedule "*/5 * * * *" health-check` registers a recurring task; `norman tasks list` shows next-run time.
- [ ] Scheduled task fires automatically, executes via OpenShell sandbox, pushes result to connected WebSocket clients.
- [ ] Terminal bell rings on task completion (configurable).
- [ ] Slack webhook POST succeeds (with valid URL); logs warning if configured but unreachable.
- [ ] Rate limiting enforced: 10 notifications/minute max across all tasks.
- [ ] `norman tasks remove <id>` unregisters and stops a running schedule.

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| MCP endpoint unavailable at skill activation time | Non-blocking connect with retry (3×, 1s backoff); fall back to static tools from SKILL.md |
| Cron expression parsing errors | Validate on registration; log warning with fix suggestion; don't crash scheduler loop |
| Slack webhook failures (network issues) | Queue notifications in memory buffer (max 100); retry with exponential backoff; degrade to terminal bell only |
| Multiple tasks firing simultaneously → notification spam | Rate limiter + coalescing: group identical task results into single notification |
| go-sdk MCP client lifecycle leaks | Ensure `Deactivate()` always calls `client.Close()`; use defer in Activate path |

---

## Dependencies on Other Steps

- **Step 5b** (agent loop): Budget checks must not block scheduler tick goroutine.
- **Step 6** (wiki engine): Compressed session archives could be tagged with `type: task` + `tags: [scheduled]`.
- **Step 7** (budget/compression): Notification-heavy sessions should count toward budget; compressed sessions should suppress redundant notifications.

---

## Libraries to Add

| Library | Purpose |
|---------|---------|
| `github.com/modelcontextprotocol/go-sdk/mcp` | MCP protocol client (official, Google-backed) |
| `github.com/robfig/cron/v3` | Cron expression parsing + scheduling |
| `github.com/slack-go/slack` | Slack webhook POST (lightweight, no OAuth needed for simple notifications) |

---

*This is a plan draft. Actual implementation will follow approved sub-steps.*
