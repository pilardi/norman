# Norman — Client ↔ Agent Server API Specification (MVP)

> This spec defines the machine-readable contract between clients (CLI, Web UI, custom) and the Agent Server. It is designed to drive code generation for both client protocol libraries and server endpoint implementations.
> Derived from `api-spec.yaml` (OpenAPI 3.1.0). Last synced: 2026-10-18.

---

## 1. Protocol Overview

| Direction | Transport | Purpose |
|-----------|-----------|---------|
| Client → Server | HTTP/REST + WebSocket | Requests, status polling, task creation |
| Server → Client | WebSocket (push) | Real-time notifications, progress events |
| Environment → Server | HTTP POST + WebSocket | Task completion reports from OpenShell sessions |
| Server → Channels | HTTP POST | Notification routing (Slack, terminal bell) |

**Transport defaults:** HTTP on port `8080`, WebSocket at `/ws`. Configurable via `~/.norman/config.yaml`.

---

## 2. REST Endpoints (Client → Server)

### 2.1 Workspace Management

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/workspaces` | Create a new workspace |
| `GET` | `/api/workspaces` | List all workspaces |
| `GET` | `/api/workspaces/{name}` | Get workspace details |
| `PATCH` | `/api/workspaces/{name}` | Partial update of workspace definition |
| `DELETE` | `/api/workspaces/{name}` | Delete a workspace |

**Request (Create):**
```json
{
  "name": "my-project",
  "path": "/home/user/projects/my-project",
  "purpose": "API migration for X service",
  "scope": ["api-gateway", "user-service"],
  "tags": ["grpc", "migration", "backend"]
}
```

**Response (201 Created):**
```json
{
  "id": "ws_abc123",
  "name": "my-project",
  "path": "/home/user/projects/my-project",
  "purpose": "API migration for X service",
  "scope": ["api-gateway", "user-service"],
  "tags": ["grpc", "migration", "backend"],
  "created_at": "2026-10-13T08:00:00Z",
  "updated_at": "2026-10-13T08:00:00Z",
  "status": "active"
}
```

**Request (Update — PATCH):**
```json
{
  "purpose": "Updated: API migration + auth integration",
  "tags": ["grpc", "migration", "backend", "auth"]
}
```

> Note: Only non-null fields are applied (partial update). Omitted fields retain their current values.

### 2.2 Session Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/sessions` | List active sessions |
| `POST` | `/api/sessions` | Create a new session (for workspace) |
| `GET` | `/api/sessions/{id}` | Get session status and details |
| `POST` | `/api/sessions/{id}` | Send a message to an active session (chat input) |
| `DELETE` | `/api/sessions/{id}` | Delete a session |
| `GET` | `/api/sessions/{id}/history` | Get conversation history turns for a session |
| `POST` | `/api/sessions/archive` | Archive a session's task data for long-term storage |

**Request (Create Session):**
```json
{
  "workspace_id": "ws_abc123",
  "client_type": "cli"
}
```

**Response (Get Session Details):**
```json
{
  "id": "sess_xyz789",
  "name": "My Chat Session",
  "workspace_id": "ws_abc123",
  "status": "active",
  "created_at": "2026-10-13T08:05:00Z",
  "last_message_at": "2026-10-13T08:10:00Z",
  "token_budget_used": 4500,
  "token_budget_remaining": 95500,
  "active_skills": ["docker-skill", "git-helper"]
}
```

**Request (Send Message):**
```json
{
  "message": "Write a test file for the API endpoint",
  "attach_tools": true
}
```

**Response (Agent Response):**
```json
{
  "session_id": "sess_xyz789",
  "response": "I'll create a test file...",
  "tools_used": ["shell", "file"],
  "task_created": false,
  "token_usage": {
    "prompt_tokens": 120,
    "completion_tokens": 340,
    "total_tokens": 460
  }
}
```

**Request (Archive Session):**
```json
{
  "task_id": "task_abc123"
}
```

### 2.3 Task Management

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/tasks` | Create a new task (user prompt) |
| `GET` | `/api/tasks` | List active/scheduled tasks (filter by status) |
| `GET` | `/api/tasks/{id}` | Get task status and details |
| `GET` | `/api/tasks/{id}/log` | Tail task output (for CLI polling) |
| `POST` | `/api/tasks/{id}/complete` | Report task completion (internal) |

**Request (Create Task):**
```json
{
  "workspace_id": "ws_abc123",
  "session_id": "sess_xyz789",
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

**Request (Report Completion):**
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

### 2.4 Skills Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/skills` | List discovered skills (metadata only) |
| `POST` | `/api/skills/paths` | Add discovery paths for skill scanning |
| `DELETE` | `/api/skills/{name}` | Remove a skill |

**Request (Add Discovery Paths):**
```json
{
  "name": "monitoring.check-endpoint"
}
```

### 2.5 Scheduled Tasks

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/tasks/scheduled` | List scheduled tasks |

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

### 2.6 Notifications

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/notifications` | List notification entries |
| `POST` | `/api/notifications` | Create a new notification |
| `DELETE` | `/api/notifications/{id}` | Delete a notification entry |

**Request (Create Notification):**
```json
{
  "title": "Build failed",
  "body": "The CI pipeline for PR #42 failed.",
  "severity": "high",
  "task_id": "task_xyz789"
}
```

### 2.7 System Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check with scheduler status |
| `GET` | `/api/config` | Get system configuration (with optional workspace merge) |
| `PUT` | `/api/config` | Update system configuration (partial update) |

**Response (Health):**
```json
{
  "status": "healthy",
  "scheduler": {
    "registered_tasks": 3,
    "last_tick": "2026-10-18T21:45:00Z"
  },
  "uptime_seconds": 86400
}
```

---

## 3. WebSocket Events (Server → Client Push)

The `/ws` endpoint pushes typed events to connected clients:

| Event | Payload Fields | Description |
|-------|---------------|-------------|
| `task_progress` | `{ task_id, message, step }` | Agent is working on a step |
| `task_complete` | `{ task_id, status, duration_ms, output_path }` | Task finished |
| `task_failed` | `{ task_id, error, retry_count }` | Task failed after retries |
| `notification` | `{ channel, message, priority }` | Push notification to client |
| `session_event` | `{ session_id, type, workspace_id, details }` | Session lifecycle event |
| `session_compressed` | `{ session_id }` | Session context was compressed |
| `context_budget_warning` | `{ used_tokens, remaining_tokens, threshold }` | Budget at 75% or 90% |

---

## 4. Environment → Server Task Completion Message

Tasks running in OpenShell sessions report completion via HTTP POST to:

```
POST /api/tasks/{task_id}/complete
Content-Type: application/json
```

**Payload (success):**
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

**Payload (failure):**
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
    SessionID   string `json:"session_id"`
    Prompt      string `json:"prompt"`
    Priority    string `json:"priority" default:"normal"` // "normal" | "high" | "low"
}
```

### 5.3 ScheduledTaskRequest
```go
type ScheduledTaskRequest struct {
    Name       string            `json:"name"`
    WorkspaceID string           `json:"workspace_id"`
    Schedule   string            `json:"schedule"` // cron expression (5 or 6 field)
    Skill      string            `json:"skill"`
    Config     map[string]string `json:"config,omitempty"`
    OnSuccess  *NotificationConfig `json:"on_success,omitempty"`
    OnFailure  *NotificationConfig `json:"on_failure,omitempty"`
}
```

### 5.4 NotificationEvent (WebSocket push)
```go
type NotificationEvent struct {
    EventType       string            `json:"event"` // "task_progress" | "task_complete" | "task_failed" | "notification" | "session_event" | "session_compressed" | "context_budget_warning"
    TaskID          string            `json:"task_id,omitempty"`
    SessionID       string            `json:"session_id,omitempty"`
    Message         string            `json:"message,omitempty"`
    Step            int               `json:"step,omitempty"`
    Channel         string            `json:"channel,omitempty"`
    Priority        string            `json:"priority,omitempty"` // "normal" | "high"
    UsedTokens      int               `json:"used_tokens,omitempty"`
    RemainingTokens int               `json:"remaining_tokens,omitempty"`
    Threshold       string            `json:"threshold,omitempty"` // "75%" | "90%"
}
```

### 5.5 Workspace
```go
type Workspace struct {
    ID        string     `json:"id"`
    Name      string     `json:"name"`
    Path      string     `json:"path"`
    Purpose   *string    `json:"purpose,omitempty"`
    Scope     []string   `json:"scope,omitempty"`
    Tags      []string   `json:"tags,omitempty"`
    CreatedAt string     `json:"created_at"`
    UpdatedAt string     `json:"updated_at"`
    Status    string     `json:"status"` // "active" | "archived"
}
```

**WorkspaceUpdateRequest:**
```go
type WorkspaceUpdateRequest struct {
    Purpose *string  `json:"purpose,omitempty"`
    Scope   []string `json:"scope,omitempty"`
    Tags    []string `json:"tags,omitempty"`
}
```

> Only non-nil fields are applied on PATCH (partial update). Omitted fields retain current values.

### 5.6 SessionStatus (SessionDetail)
```go
type SessionStatus struct {
    ID                string   `json:"id"`
    Name              string   `json:"name"`
    WorkspaceID       string   `json:"workspace_id"`
    Status            string   `json:"status"` // "active" | "inactive" | "crashed" | "recovered"
    CreatedAt         string   `json:"created_at"`
    LastMessageAt     *string  `json:"last_message_at,omitempty"`
    TokenBudgetUsed   int      `json:"token_budget_used"`
    TokenBudgetRemaining int  `json:"token_budget_remaining"`
    ActiveSkills      []string `json:"active_skills"`
}
```

### 5.7 AgentResponse
```go
type AgentResponse struct {
    SessionID   string   `json:"session_id"`
    Response    string   `json:"response"`
    ToolsUsed   []string `json:"tools_used"`
    TaskCreated bool     `json:"task_created"`
    TaskID      *string  `json:"task_id,omitempty"`
    TokenUsage  TokenUsage `json:"token_usage"`
}
```

### 5.8 SessionTurn
```go
type SessionTurn struct {
    Role      string  `json:"role"` // "user" | "assistant" | "system"
    Content   string  `json:"content"`
    Timestamp *string `json:"timestamp,omitempty"`
}
```

### 5.9 TokenUsage
```go
type TokenUsage struct {
    PromptTokens    int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens     int `json:"total_tokens"`
}
```

### 5.10 TaskCompletion
```go
type TaskCompletion struct {
    TaskID     string      `json:"task_id"`
    Status     string      `json:"status"` // "completed" | "failed"
    DurationMs int         `json:"duration_ms"`
    OutputPath *string     `json:"output_path,omitempty"`
    Error      *string     `json:"error,omitempty"`
    RetryCount int         `json:"retry_count" default:"0"`
    Metadata   map[string]any `json:"metadata,omitempty"`
}
```

### 5.11 WorkspaceConfig
```go
type WorkspaceConfig struct {
    BaseDir       string   `json:"base_dir"`
    Model         string   `json:"model"`
    MaxTokens     int      `json:"max_tokens"`
    Temperature   float32  `json:"temperature"`
    SkillOverrides []string `json:"skill_overrides"`
    SystemPrompt  string   `json:"system_prompt"`
}
```

### 5.12 SystemConfig
```go
type SystemConfig struct {
    DefaultModel string         `json:"default_model"`
    ServerPort   int            `json:"server_port"`
    Host         string         `json:"host"`
    Skills       SkillsConfig   `json:"skills"`
    Notify       NotifyConfig   `json:"notify"`
    Logging      map[string]string `json:"logging"`
    Temperature  float32        `json:"temperature"`
    MaxTokens    int            `json:"max_tokens"`
    OpenShell    OpenShellConfig `json:"openshell"`
}
```

### 5.13 HealthStatus
```go
type HealthStatus struct {
    Status       string         `json:"status"` // "healthy" | "degraded"
    Scheduler    SchedulerStatus `json:"scheduler"`
    UptimeSeconds int           `json:"uptime_seconds"`
}
```

### 5.14 SchedulerStatus
```go
type SchedulerStatus struct {
    RegisteredTasks int     `json:"registered_tasks"`
    LastTick        *string `json:"last_tick,omitempty"`
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

*This spec was derived from `requirements.md`, `project.md`, and `api-spec.yaml`. It formalizes the scattered API descriptions into a single machine-readable contract suitable for code generation.*
