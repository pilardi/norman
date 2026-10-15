# Step 5 — Agent Server + Task API implementation

> **Status:** ✅ COMPLETE (merged to `main`)

---

## Problem Statement

We need a running HTTP server that exposes task management endpoints, executes tasks via an agent loop, and pushes real-time events to connected WebSocket clients. This is the core runtime of the system: it orchestrates skill activation, sandbox provisioning, tool execution, and result delivery.

---

## Current State (at time of planning)

- `cmd/norman-server/main.go` — empty placeholder; no routes registered.
- `pkg/protocol/` — generated types (`types.go`, `client.go`) but no server-side handlers.
- No agent loop, no task store, no WebSocket hub.
- OpenShell SDK client is a stub; needs real integration.

---

## Proposed Architecture

### 1. Step 5a — Agent Server Skeleton & Protocol Wiring

| File | Purpose |
|------|---------|
| `cmd/norman-server/main.go` | Bootstrap Echo server; register handlers from `internal/api/handlers.go`; start WebSocket hub at `/ws`. |
| `pkg/protocol/client.go` + `types.go` | Generated client library + types used by server's internal state management. |
| `internal/api/handlers.go` | Auto-generated HTTP handler stubs (one per endpoint in `api-spec.yaml`). |

**Acceptance:** `norman server --port 8080` starts; `curl /health` returns 200; WebSocket at `/ws` accepts connections and pushes mock events.

---

### 2. Step 5b — Task API + Execution Engine

| File | Purpose |
|------|---------|
| `pkg/agent/loop.go` | Core agent loop: task submission, execution lifecycle (resolve→create sandbox→waitReady→execute→cleanup), retry logic (3× exponential backoff). |
| `pkg/memory/store.go` | File-based persistence with optimistic locking; save/load/delete tasks and sessions; prune expired entries. |
| `cmd/norman-server/main.go` | All 20 handlers wired to real implementations (not stubs); server struct holds Loop + Store + WSHub. |
| `pkg/skills/activate.go` | Added `Registry()` method for agent loop access. |

**Acceptance:** Server starts, `/health` returns 200; `/api/tasks` returns task list; `POST /api/tasks` creates and executes a task; WebSocket clients receive real-time progress events. Build passes (`go build ./cmd/norman-server/`), `go vet` clean on all packages.

---

### 3. API Spec & Code Generation (shared by 5a + 5b)

> **Status:** ✅ COMPLETE

- `api-spec.yaml` (OpenAPI 3.1) is the single source of truth for all client-server protocol types, endpoints, and WebSocket event schemas.
- Regenerated via `make generate` → produces `pkg/protocol/types.go`, `pkg/protocol/client.go`, and `internal/api/handlers.go`.
- Verified: generated code is up-to-date with spec (zero diff).

---

## Data Model

```go
// pkg/protocol/types.go (generated)
type TaskCreateRequest struct {
    Prompt     string   `json:"prompt"`
    WorkspaceId *string  `json:"workspace_id,omitempty"`
}

type TaskResult struct {
    TaskID      string `json:"task_id"`
    Status      string `json:"status"` // "running", "completed", "failed"
    Output      string `json:"output,omitempty"`
    Error       string `json:"error,omitempty"`
    CompletedAt time.Time `json:"completed_at"`
}

type WebSocketEvent struct {
    Event   string  `json:"event"`     // "task_progress", "task_complete", "task_failed"
    Message *string `json:"message,omitempty"`
    Step    *int    `json:"step,omitempty"`
}
```

---

## Integration Points

| Component | Change |
|-----------|--------|
| `pkg/agent/loop.go` | New file — core agent loop with task lifecycle management. |
| `pkg/memory/store.go` | Add Task and Session persistence; optimistic locking on writes. |
| `cmd/norman-server/main.go` | Wire all 20 handlers to real implementations; inject Loop + Store + WSHub into server struct. |
| `pkg/skills/activate.go` | Expose `Registry()` method for agent loop access. |
| `api-spec.yaml` | Defines all endpoints, request/response schemas, WebSocket event types. |

---

## Acceptance Criteria

- [ ] `norman server --port 8080` starts and responds to `/health`.
- [ ] `POST /api/tasks` creates a task that executes via OpenShell sandbox.
- [ ] WebSocket clients receive `task_progress`, `task_complete`, `task_failed` events in real time.
- [ ] Task persistence survives server restart (via `pkg/memory/store.go`).
- [ ] Build passes: `go build ./cmd/norman-server/` and `go vet ./...` clean.

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Agent loop blocks HTTP handler goroutines | Run task execution in background goroutine; return immediately with `TaskID`. |
| WebSocket client disconnects mid-task | Hub tracks active connections; broadcasts events to all connected clients. |
| OpenShell sandbox creation fails | Retry with exponential backoff (3×); surface error via `task_failed` event. |

---

## Dependencies on Other Steps

- **Step 6** (wiki engine): Task results can optionally create wiki entries (`type: task`).
- **Step 7** (budget/compression): Agent loop budget checks integrate into task execution lifecycle.
- **Step 8** (MCP/scheduler): Scheduler uses the same WSHub for real-time push events.

---

*This is a plan draft. Actual implementation follows approved sub-steps.*
