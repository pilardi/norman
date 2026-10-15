# Session Workflow Proposal

> **Status:** Partially Implemented (see ✅ / ⬜ markers below)  
> **Date:** 2026-10-15  
> **Author:** Pablo (via goose)

---

## 1. Problem Statement

The original Norman CLI included two commands that blurred the boundary between *workspace context* and *interactive agent interaction*:

| Command | Original Intent | Why it was a problem |
|---------|----------------|----------------------|
| `switch <name>` | Select a workspace as "current" context (like `cd`) | Redundant — clients should connect to sessions, not manage a global cwd |
| `run [prompt]` | Fire-and-forget prompt execution | One-shot; no session identity, no real-time feedback, no conversation continuity |

The architectural insight: **a session is an interactive conversation about a particular workspace using a model.** Everything should happen *within* sessions. No separate fire-and-forget prompt execution. No global "current workspace" concept at the CLI level — clients connect to sessions and send prompts iteratively.

---

## 2. Proposed Session Lifecycle

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────────────┐
│  Create      │────▶│  Subscribe (WS)  │────▶│  Send Prompts       │
│  Session     │     │  /ws?session_id  │     │  POST /api/tasks    │
└─────────────┘     └──────────────────┘     └─────────────────────┘
                         ▲                          │
                         │                          ▼
                    TaskComplete/                  Receive real-time
                    TaskFailed events              progress, tool calls,
                                                   completion/failure
```

### 2.1 Session Creation ⬜ (auto-created on first task; explicit `POST /api/sessions` not yet implemented)

**Request:** `POST /api/sessions`
```json
{
  "workspace_id": "<uuid>",
  "client_type": "custom"
}
```

- `workspace_id`: binds the session to a specific workspace's OpenShell sandbox.
- `client_type`: `"cli"`, `"web_ui"`, or `"custom"` (programmatic SDK/API usage).

**Response:** `SessionId` + initial status (`"created"`). ⬜

### 2.2 WebSocket Subscription ✅

**Request:** `GET /ws?session_id=<id>`

The client subscribes to a push-based event stream for this session:

| Event | Payload |
|-------|---------|
| `TaskProgress` | `{task_id, message, level}` — streaming feedback during execution ✅ |
| `TaskComplete` | `{task_id, result}` — single prompt finished successfully ✅ |
| `TaskFailed` | `{task_id, error}` — prompt failed with details ✅ |
| `SessionEvent` | `{event_type: "created" \| "closed"}` — lifecycle notifications ✅ |

### 2.3 Iterative Prompt Submission ✅ (enhanced with `session_id`)

**Request:** `POST /api/tasks`
```json
{
  "workspace_id": "<uuid>",
  "prompt": "What files are in the attached directory?",
  "session_id": "<id>"  // NEW — optional, ties task to a session
}
```

Each call creates a new task *within* the session's context. The agent loop:
1. Resolves skills for the prompt ✅
2. Injects raw sources from the workspace ✅
3. Executes against the OpenShell sandbox ✅
4. Returns progress via WS events ✅

### 2.4 Session Termination

- **Explicit:** `DELETE /api/sessions/<id>` ✅ (implemented, wired up)
- **Implicit:** After N minutes of idle (configurable), the session auto-cleans up. ⬜ (not yet implemented)

---

## 3. API Contract Changes

### 3.1 New Fields on `TaskCreateRequest` ✅

```go
type TaskCreateRequest struct {
    WorkspaceId string `json:"workspace_id"` // existing
    Prompt      string `json:"prompt"`       // existing
    SessionId   *string `json:"session_id,omitempty"` // NEW: ties task to a session for conversation continuity
}
```

This allows tasks from the same session to share conversation context (growing window). Without `session_id`, each task is stateless.

### 3.2 New Endpoint: `/api/sessions`

| Method | Path | Description | Status |
|--------|------|-------------|--------|
| POST | `/api/sessions` | Create a new session bound to a workspace | ⬜ (auto-created on demand) |
| GET | `/api/sessions` | List active sessions (optionally filtered by `workspace_id`) | ✅ |
| DELETE | `/api/sessions/:id` | Terminate a session | ✅ |
| GET | `/ws?session_id=:id` | WebSocket subscription for real-time events | ✅ |

### 3.3 Removed CLI Commands ✅

| Command | Replacement Workflow |
|---------|---------------------|
| `switch <name>` | → Connect to a session bound to that workspace |
| `run [prompt]` | → `POST /api/tasks {workspace_id, prompt}` via WS subscription |

The remaining Norman CLI commands (`create`, `list`, `config`, `skills`, `sessions`, `tasks`) are now purely **workspace management** — no interactive agent execution.

---

## 4. Client-Side Interaction Model

### 4.1 Pseudocode (SDK/Programmatic Client)

```go
// 1. Create session bound to workspace (auto-created if none exists)
session := createSession(workspaceID, "custom")

// 2. Subscribe to WS events
events := subscribeWS(session.ID)

// 3. Interactive prompt loop
for {
    prompt := readUserInput()
    task := createTask(session.WorkspaceID, prompt)
    
    // Watch events until this task completes
    for event := range events {
        if event.TaskID == task.ID {
            handleEvent(event)
            if event.Type == "TaskComplete" || event.Type == "TaskFailed" {
                break
            }
        }
    }
}

// 4. Graceful shutdown
closeSession(session.ID)
```

### 4.2 CLI Client (Simplified) ⬜ (not yet implemented)

The `norman` binary no longer has `switch` or `run`. Proposed CLI wrappers:

```bash
# Create a session for a workspace
norman sessions create --workspace my-project

# Send prompts into that session (the client handles WS internally)
norman tasks send "What files are in the attached directory?"

# List active sessions
norman sessions list

# Close a session
norman sessions close <session-id>
```

The CLI becomes a thin wrapper around the REST API + WS subscription, not a separate execution engine.

---

## 5. Migration Path

### From Old Workflow to New ✅

| Old | New |
|-----|-----|
| `norman switch my-project` | `norman sessions create --workspace my-project` → get session ID |
| `norman run "explain this code"` | `norman tasks send "explain this code"` (same session, WS events streamed) |
| Global "current workspace" concept | Session-bound to workspace — no global cwd needed |

### Breaking Changes ✅

- `switch <name>` removed from CLI
- `run [prompt]` removed from CLI
- `POST /api/tasks` gains optional `session_id` field (backward-compatible if omitted)
- WS subscription is per-session, not a single global stream

---

## 6. Open Questions for Review ⬜ (resolved in implementation)

1. **Auto-create sessions?** ✅ Resolved: tasks auto-create a session for the workspace if none exists. Explicit `POST /api/sessions` remains as an option.
2. **Conversation continuity?** ✅ Resolved: `session_id` field on `TaskCreateRequest` enables growing context window tied to a session.
3. **Idle timeout?** ⬜ Not yet implemented. Configurable via server config.

---

## 7. Files Affected (Implementation Status)

| File | Change | Status |
|------|--------|--------|
| `pkg/protocol/types.go` | Add `SessionId *string` field to `TaskCreateRequest` | ✅ Done |
| `pkg/agent/loop.go` | Accept `sessionID` in `Submit()`, add `SessionId` to `Task` struct | ✅ Done |
| `cmd/norman-server/main.go` | Auto-create session on first task; `DeleteSession` handler; `CreateSession` fix; idle cleanup loop | ✅ Done |
| `internal/api/handlers.go` | `DeleteSession` in interface + wrapper method | ✅ Done |
| `pkg/memory/store.go` | `DeleteSession()` and `ListSessions()` methods | ✅ Done |
| `cmd/norman/main.go` | Remove `switch`/`run`; add thin `sessions create/close`, `tasks send` wrappers | ✅ Done |

---

## 8. Implementation Summary (2026-10-15)

### Completed in `feat/session-workflow` branch:
- ✅ `SessionId` field added to `TaskCreateRequest`
- ✅ `Loop.Submit()` accepts `sessionID` parameter
- ✅ `Task` struct has `SessionId` field
- ✅ Auto-create session logic in `CreateTask` handler
- ✅ `DeleteSession` handler + route wired up
- ✅ `ListSessions()` and `DeleteSession()` methods on store
- ✅ `ServerInterface` and `ServerInterfaceWrapper` updated
- ✅ All builds clean; tests pass
- ✅ `POST /api/sessions` endpoint now persists session and returns Session object (was previously a stub)
- ✅ Idle timeout auto-cleanup goroutine (30 min default, 5 min scan interval)
- ✅ CLI wrapper commands: `norman sessions create --workspace <name>`, `sessions close <id>`, `tasks send <prompt>`

---

*This is a living document. Revisions welcome as implementation progresses.*
