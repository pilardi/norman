# Norman — Client ↔ Agent Server API Specification (MVP)

> This spec defines the machine-readable contract between clients (CLI, Web UI, custom) and the Agent Server. It is designed to drive code generation for both client protocol libraries and server endpoint implementations.

---

## 1. Protocol Overview

| Direction | Transport | Purpose |
|-----------|-----------|---------|
| Client → Server | HTTP/REST + WebSocket | Requests, status polling, task creation |
| Server → Client | WebSocket (push) | Real-time notifications, progress events |
| Environment → Server | HTTP POST + WebSocket | Task completion reports from OpenShell sessions |
| Server → Channels | HTTP POST | Notification routing (Slack, terminal bell) |

**Transport defaults:** HTTP on port `8080`, WebSocket at `/ws`. Configurable via `~/.config/norman/config.yaml`.

---

## 2. REST Endpoints (Client → Server)

### 2.1 Workspace Management

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/workspaces` | Create a new workspace |
| `GET` | `/api/workspaces` | List all workspaces |
| `GET` | `/api/workspaces/{name}` | Get workspace details |
| `DELETE` | `/api/workspaces/{name}` | Delete a workspace |

**Request (Create):**
```json
{
  "name": "my-project",
  "path": "/home/user/projects/my-project"
}
```

**Response (201 Created):**
```json
{
  "id": "ws_abc123",
  "name": "my-project",
  "path": "/home/user/projects/my-project",
  "created_at": "2026-10-13T08:00:00Z",
  "status": "active"
}
```

### 2.2 Session Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/sessions` | List active sessions |
| `GET` | `/api/sessions/{id}` | Get session details |
| `POST` | `/api/sessions` | Create a new session (for workspace) |

**Request (Create):**
```json
{
  "workspace_id": "ws_abc123",
  "client_type": "cli"
}
```

### 2.3 Task Management

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/tasks` | Create a new task (user prompt) |
| `GET` | `/api/tasks` | List active/scheduled tasks |
| `GET` | `/api/tasks/{id}` | Get task status and details |
| `DELETE` | `/api/tasks/{id}` | Cancel a running task |
| `GET` | `/api/tasks/{id}/log` | Tail task output (for CLI polling) |

**Request (Create Task):**
```json
{
  "workspace_id": "ws_abc123",
  "prompt": "Write a test file for the API endpoint",
  "priority": "normal"
}
```

**Response (201 Created):**
```json
{
  "id": "task_xyz789",
  "workspace_id": "ws_abc123",
  "status": "running",
  "prompt": "Write a test file for the API endpoint",
  "created_at": "2026-10-13T08:05:00Z"
}
```

### 2.4 Skills Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/skills` | List discovered skills (metadata only) |
| `POST` | `/api/skills/install` | Install a skill from discovery path |
| `DELETE` | `/api/skills/{name}` | Remove a skill |

### 2.5 Scheduled Tasks

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/tasks/schedule` | Register a recurring task |
| `GET` | `/api/tasks/scheduled` | List scheduled tasks |
| `DELETE` | `/api/tasks/scheduled/{id}` | Remove a scheduled task |

**Request (Schedule):**
```json
{
  "name": "check-api-health",
  "workspace_id": "ws_abc123",
  "schedule": "*/5 * * * *",
  "skill": "monitoring.check-endpoint",
  "config": {
    "url": "${secret:api-url}",
    "timeout": "30s"
  },
  "on_success": { "channel": "slack", "name": "#ops" },
  "on_failure": { "channel": "slack", "name": "#ops", "priority": "high" }
}
```

---

## 3. WebSocket Events (Server → Client Push)

The `/ws` endpoint pushes typed events to connected clients:

| Event | Payload | Description |
|-------|---------|-------------|
| `task_progress` | `{ task_id, message, step }` | Agent is working on a step |
| `task_complete` | `{ task_id, status, duration_ms, output_path }` | Task finished |
| `task_failed` | `{ task_id, error, retry_count }` | Task failed after retries |
| `notification` | `{ channel, message, priority }` | Push notification to client |
| `session_event` | `{ type, workspace_id, details }` | Session lifecycle event (created, crashed, recovered) |
| `context_budget_warning` | `{ used_tokens, remaining_tokens, threshold }` | Budget at 75% or 90% |

---

## 4. Environment → Server Task Completion Message

Tasks running in OpenShell sessions report completion via HTTP POST to:

```
POST /api/tasks/{task_id}/complete
Content-Type: application/json
```

**Payload:**
```json
{
  "task_id": "task_xyz789",
  "status": "completed",
  "duration_ms": 134000,
  "output_path": "./reports/summary.md",
  "metadata": {
    "rows_processed": 1542,
    "files_created": ["test_api.go"],
    "skills_used": ["docker-skill"]
  }
}
```

**Error variant:**
```json
{
  "task_id": "task_xyz789",
  "status": "failed",
  "error": "MCP connection timeout after 30s",
  "retry_count": 2,
  "duration_ms": 45000
}
```

---

## 5. Message Types (Go Structs — for code generation)

These types drive the generated client protocol library and server handlers:

### 5.1 TaskMessage
```go
type TaskMessage struct {
    TaskID     string            `json:"task_id"`
    Status     string            `json:"status"` // "running" | "completed" | "failed" | "cancelled"
    DurationMs int64             `json:"duration_ms,omitempty"`
    OutputPath string            `json:"output_path,omitempty"`
    Error      string            `json:"error,omitempty"`
    RetryCount int               `json:"retry_count,omitempty"`
    Metadata   map[string]string `json:"metadata,omitempty"`
}
```

### 5.2 TaskRequest (Create)
```go
type TaskRequest struct {
    WorkspaceID string `json:"workspace_id"`
    Prompt      string `json:"prompt"`
    Priority    string `json:"priority" default:"normal"` // "normal" | "high" | "low"
}
```

### 5.3 ScheduledTaskRequest
```go
type ScheduledTaskRequest struct {
    Name       string            `json:"name"`
    WorkspaceID string           `json:"workspace_id"`
    Schedule   string            `json:"schedule"` // cron expression
    Skill      string            `json:"skill"`
    Config     map[string]string `json:"config,omitempty"`
    OnSuccess  *NotificationConfig `json:"on_success,omitempty"`
    OnFailure  *NotificationConfig `json:"on_failure,omitempty"`
}
```

### 5.4 NotificationEvent (WebSocket push)
```go
type NotificationEvent struct {
    EventType string            `json:"event"` // "task_progress" | "task_complete" | "task_failed" | "notification" | "session_event" | "context_budget_warning"
    TaskID    string            `json:"task_id,omitempty"`
    Message   string            `json:"message,omitempty"`
    Step      int               `json:"step,omitempty"`
    Channel   string            `json:"channel,omitempty"`
    Priority  string            `json:"priority,omitempty"` // "normal" | "high"
    UsedTokens int              `json:"used_tokens,omitempty"`
    RemainingTokens int          `json:"remaining_tokens,omitempty"`
    Threshold string            `json:"threshold,omitempty"` // "75%" | "90%"
}
```

### 5.5 Workspace
```go
type Workspace struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Path      string `json:"path"`
    CreatedAt string `json:"created_at"`
    Status    string `json:"status"` // "active" | "archived"
}
```

---

## 6. Generated Code Targets

This spec is designed to produce:

| Target | Tool/Generator | Output |
|--------|---------------|--------|
| Go client protocol library | `oapi-codegen` or custom generator | `pkg/protocol/types.go`, `pkg/protocol/client.go` |
| Go server handlers | `oapi-codegen` or custom generator | `internal/api/handlers.go` (stub implementations) |
| WebSocket message types | Custom codegen | `pkg/protocol/events.go` |
| Task completion schema | JSON Schema → Go structs | `pkg/protocol/task_completion.go` |

---

## 7. Constraints & Notes

- **No authentication at MVP** — any client connecting to the server is treated as "the user." The `X-User-ID` header is optional but ignored.
- **Rate limiting**: max 10 notifications/minute across all tasks (server-side enforcement).
- **Task stateless between runs**: each scheduled task run starts fresh with current environment state unless it explicitly reads/writes workspace files.
- **Optimistic writes**: concurrent clients/tasks write to `.norman/` files via lock file (`memory/.lock`). Conflicts detected at read time.
- **Version compatibility**: server logs warning if OpenShell < 0.1.0; degrades gracefully.

---

*This spec was derived from `requirements.md` and `project.md`. It formalizes the scattered API descriptions into a single machine-readable contract suitable for code generation.*
