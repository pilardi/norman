# Norman — Implementation Roadmap

> Generated: 2026-10-16 · Branch: `impl/stubs-real-implementations`

## What's Done (MVP)

| Area | Status | Notes |
|------|--------|-------|
| Agent loop (session lifecycle, budget compression, skill resolution) | ✅ Real | Full implementation in `pkg/agent/loop.go` |
| Memory store (atomic JSON writes) | ✅ Real | `pkg/memory/store.go` |
| Raw sources snapshot store | ✅ Real | `pkg/memory/raw_sources.go` |
| Skill discovery & activation | ✅ Real | `pkg/skills/discover.go`, `activate.go` |
| MCP client (SSE transport) | ✅ Real | `pkg/mcp/client.go` |
| OpenShell SDK client (mTLS, sandbox CRUD) | ✅ Real | `pkg/env/client.go`, `sdk_adapter.go` |
| Wiki engine (git versioning, stale pruning) | ✅ Real | `pkg/wiki/engine.go`, `entry.go`, `version.go` |
| Token budget tracking & compression | ✅ Real | `pkg/budget/budget.go`, `compressor.go` |
| Multi-channel notifications (WS push, bell, Slack webhook) | ✅ Real | `pkg/notify/notifier.go` |
| Cron-based scheduler tick loop | ✅ Real | `pkg/scheduler/scheduler.go` |
| OpenAPI routes (oapi-codegen/v2 generated) | ✅ Real | `internal/api/handlers.go` |
| WebSocket hub (real-time push events) | ✅ Real | `WSHub` in main.go |

## Stub → Real Implementations (just completed)

| Handler / Function | Before | After |
|---|---|---|
| `ListScheduledTasks` | Returned empty array `[]map[string]interface{}{}` | Queries `scheduler.List()`, converts to `protocol.ScheduledTask` with UUID parsing and status mapping |
| `TailTaskLog` | Hardcoded `["sandbox initialized", "ready"]` | Calls `loop.GetTask(id)` → `GetLogs(ctx, sandboxID)` via OpenShell client |
| `runCommand()` in scheduler | Returned `fmt.Errorf("exec not yet connected to OpenShell")` | Wired to `env.Client.RunCommand()` via `WithRunCommand()` adapter; falls back to local exec if not wired |

## Remaining Work (Priority Order)

### P0 — Critical Path

1. **Scheduler → Agent Task ID mapping**
   - Currently `TailTaskLog` looks up tasks by agent task ID (`s.loop.GetTask(id)`).
   - The API spec's `GET /api/tasks/:id/logs` expects a scheduler task ID or agent task ID — clarify which and add an ID resolver if needed.

2. **Scheduler command execution in sandboxes**
   - `runCommand()` now uses OpenShell Exec(), but tasks registered via `Add()` don't carry workspace/sandbox context.
   - Need to decide: should scheduled tasks run in a default sandbox, or accept a `workspace_id` field on `TaskEntry`?

3. **`ScheduleTask` handler (main.go ~line 598)**
   - Currently only broadcasts a WS event and returns 201 Created — doesn't actually register the task in the scheduler.
   - Should call `s.scheduler.Add(entry)` with proper cron parsing and validation.

4. **`UnscheduleTask` handler (main.go ~line 648)**
   - Broadcasts an event but doesn't call `s.scheduler.Remove(id)`.

### P1 — Enhancements

5. **`GetTask` returns full task state**
   - Currently maps agent `Task.Output ([]byte)` → `"output"` string field. Ensure this is correct for the protocol spec.

6. **`ReportTaskComplete` handler (main.go ~line 673)**
   - Only broadcasts a WS event; doesn't update the scheduler's task result or write to the memory store.

7. **Scheduler `OutputHandler` callback**
   - `TaskEntry.OutputHandler` is defined but never wired in the main init path. Should connect it to `s.wsHub.BroadcastEvent()` for real-time progress updates during scheduled task execution.

8. **Idle cleanup loop**
   - `idleCleanupLoop` (main.go ~line 560) reads session files directly from disk instead of using `s.store.ListSessions()`. Consider consolidating on the store API.

### P2 — Nice-to-Have

9. **Scheduler task registry persistence**
   - Tasks are in-memory only; add `store.SaveScheduledTasks()` / `LoadScheduledTasks()` for crash recovery.

10. **`TailTaskLog` streaming support**
    - Currently returns a snapshot of last N lines. For long-running tasks, consider SSE streaming of new log output.

11. **Health check includes scheduler status**
    - `GET /health` returns `{"status":"ok"}`. Could include scheduler health (tasks registered, last tick time).

12. **Workspace-aware scheduled tasks**
    - `ScheduledTaskRequest.WorkspaceId` is accepted but not used when scheduling. Wire it into the sandbox context for task execution.

## Notes

- All changes are on branch `impl/stubs-real-implementations`.
- Build and tests pass: `go build ./cmd/norman-server/` and `go test ./...` succeed.
- No new dependencies added; all changes use existing SDK types (`v1.ExecResult`).
