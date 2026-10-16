# Norman — Implementation Roadmap

> Branch: `impl/stubs-real-implementations` · Requirements: chat-first agent vision (session-centric)

## Vision Summary

Norman is a **chat agent**. You talk to it in a session within a workspace. Tasks and scheduling are *emergent* from conversation, not manually triggered. The primary interaction model is interactive conversation — everything else is secondary.

## What's Done (Stubs → Real + Spec Rewrite)

| Area | Status | Notes |
|------|--------|-------|
| Agent loop (session lifecycle, budget compression, skill resolution) | ✅ Real | `pkg/agent/loop.go` |
| Memory store (atomic JSON writes) | ✅ Real | `pkg/memory/store.go` |
| Raw sources snapshot store | ✅ Real | `pkg/memory/raw_sources.go` |
| Skill discovery & activation | ✅ Real | `pkg/skills/discover.go`, `activate.go` |
| MCP client (SSE transport) | ✅ Real | `pkg/mcp/client.go` |
| OpenShell SDK client (mTLS, sandbox CRUD) | ✅ Real | `pkg/env/client.go`, `sdk_adapter.go` |
| Wiki engine (git versioning, stale pruning) | ✅ Real | `pkg/wiki/engine.go`, `entry.go`, `version.go` |
| Token budget tracking & compression | ✅ Real | `pkg/budget/budget.go`, `compressor.go` |
| Multi-channel notifications | ✅ Real | `pkg/notify/notifier.go` |
| Cron-based scheduler tick loop | ✅ Real | `pkg/scheduler/scheduler.go` |
| OpenAPI routes (oapi-codegen/v2) | ✅ Real | `internal/api/handlers.go` |
| WebSocket hub | ✅ Real | `WSHub` in main.go |
| Stub implementations replaced | ✅ Done | `ListScheduledTasks`, `TailTaskLog`, `runCommand()` wired to OpenShell Exec() |
| Requirements spec rewritten | ✅ Done | Chat-first, session-centric (269 lines vs ~700) |
| Session API (POST /messages, GET /sessions/{id}) | ✅ Done | `api-spec.yaml`, `pkg/agent/loop.go`, `cmd/norman-server/main.go` |
| Emergent Task Creation (CreateTask, SendMessageWithTask, GET/DELETE /api/tasks/{id}) | ✅ Done | `pkg/agent/loop.go`, `cmd/norman-server/main.go`, `api-spec.yaml` |

### Phase 0 — Interactive Chat CLI (P0)

**Goal:** Provide the REPL experience that makes the chat-first model usable. This is the *primary* interaction layer — everything else exists to support it.

| CLI Command | Purpose | Notes |
|-------------|---------|-------|
| `norman chat --workspace <name>` | Enter REPL chat mode for a session | Primary interaction; blocks until session closed |
| `/help` | Show available slash-commands | In-chat help |
| `/list tasks` | List emergent scheduled tasks for current session | Read-only, in-chat |
| `/close` | Close the current session (save transcript) | Graceful shutdown |
| `/new` | Start a new chat session (auto-create or prompt) | Creates new session ID |
| `/history [N]` | Show last N messages of current session | Context review |
| `norman sessions list` | List all sessions across workspaces | Cross-workspace view |
| `norman sessions switch <name>` | Switch to another session's transcript | One active per client |
| `norman sessions archive <id>` | Archive a session (move to `.norman/archived/`) | Keeps history, removes from active list |

> **Design principle:** The REPL is the *primary* interface. All other commands (`tasks list`, `config`, `skills list`) are convenience/read-only. `norman chat` is where the agent's intelligence is exercised — planning, tool use, skill activation, memory management all happen inside the conversation loop.

---

## Codebase Update Plan (to meet new requirements)

### Phase 1 — Session-Centric API (P0)

**Goal:** Make `/api/sessions/{id}/messages` the primary interface; demote task endpoints to secondary.

| # | Change | Files | Notes |
|---|--------|-------|-------|
| 1 | Add `POST /api/sessions/{id}/messages` endpoint (chat input) | `cmd/norman-server/main.go`, `internal/api/handlers.go` | Accepts a message string; pushes to agent loop's processing queue. Returns task ID if agent creates an emergent task. |
| 2 | Add `GET /api/sessions/{id}` — session status/details | `main.go` | Returns session state: active/inactive, token budget used/remaining, last message timestamp, active skills list. |
| 3 | Deprecate/remove manual task creation endpoints (`POST /api/tasks`, `GET /api/tasks`) | `internal/api/handlers.go` | Keep read-only access; remove write endpoints. Tasks are created by the agent during conversation. |
| 4 | Update OpenAPI spec to reflect session-centric API | `openapi.yaml` (or equivalent) | Replace task-heavy endpoint list with session/message-focused routes. |
| 5 | Wire chat message → agent loop processing | `pkg/agent/loop.go`, `main.go` | Message enters the planning loop; agent responds via WS push + returns structured result to client. |

### Phase 2 — Emergent Task Creation (P1) ✅ DONE

**Goal:** Agent creates scheduled tasks during conversation based on user intent.

| # | Change | Files | Notes | Status |
|---|--------|-------|-------|--------|
| 6 | Add `Agent.CreateTask(prompt string, config map[string]string) (*Task, error)` to Loop | `pkg/agent/loop.go` | Agent calls this when it decides a recurring task is needed. Accepts config overrides (priority, workspace_id). Returns completed Task. | ✅ Implemented |
| 7 | Wire `CreateTask` into SendMessage flow via `SendMessageWithTask` | `pkg/agent/loop.go` | When `createTask=true`, after processing the message, calls CreateTask internally and includes task_created/task_id in response. | ✅ Implemented |
| 8 | Enhance `GET /api/tasks/{id}` to return proper `protocol.Task` struct | `cmd/norman-server/main.go` | Returns structured Task with UUID, status enum, prompt, workspace, timestamps. | ✅ Implemented |
| 9 | Add `DELETE /api/tasks/{id}` — permanent task removal (replaces Cancel semantics) | `cmd/norman-server/main.go`, `api-spec.yaml` | Uses `DeleteTask()` on Loop which removes from memory + persists deletion. Updated spec to reflect "permanent removal" vs "cancel". | ✅ Implemented |

### Phase 3 — Interactive Chat CLI REPL (P1) ✅ DONE

**Goal:** Provide the REPL experience that makes the chat-first model usable.

| # | Change | Files | Notes |
|---|--------|-------|-------|
| 10 | Add `norman chat --workspace <name>` REPL mode | `cmd/norman/main.go` (new command) | Reads user input line-by-line; pushes to agent loop via WS or HTTP POST `/api/sessions/{id}/messages`; displays agent responses in real-time. Blocks until session closed. ✅ Implemented |
| 11 | Implement slash-commands parser (`/help`, `/list tasks`, `/close`, `/new`, `/history`) | `cmd/norman/main.go` | Slash-commands are parsed before being sent to the agent; they trigger local actions (e.g., `/close` sends a WS close event, `/new` calls POST /api/sessions). ✅ Implemented |
| 12 | Add session persistence: save full conversation transcript to `.norman/sessions/<id>/transcript.md` | `pkg/memory/store.go`, `main.go` | Each message + agent response is appended. Supports replay and session resume. ✅ Implemented |
| 13 | Add `/sessions`, `/switch <id>`, `/archive <id>` slash-commands (in-chat) | `cmd/norman/main.go` | Session management from within REPL — list, switch, archive sessions. ✅ Implemented |
| 14 | Update `idleCleanupLoop` to use `s.store.ListSessions()` instead of direct file reads | `main.go` | Consolidate on the store API for session management. ✅ Already wired |
| 15 | Add `GET /api/sessions/:id/history` handler (server) | `cmd/norman-server/main.go` | Returns last N conversation turns as JSON for `/history` CLI command. ✅ Implemented |

### Phase 3b — Session Management Enhancements (P1) ✅ DONE

**Goal:** Sessions are the primary unit of state, not tasks.

| # | Change | Files | Notes |
|---|--------|-------|-------|
| 16 | Add `Store.SaveTranscript(sessionID, turns)` — writes markdown transcript | `pkg/memory/store.go` | Full conversation as formatted markdown with timestamps. ✅ Implemented |
| 17 | Add `Store.LoadTranscript(sessionID)` — reads markdown transcript | `pkg/memory/store.go` | For `/history` CLI command to display past sessions. ✅ Implemented |
| 18 | Wire incremental transcript saving in chat loop (user + agent turns) | `cmd/norman/main.go` | Each turn tracked and saved on `/close`. ✅ Implemented |

### Phase 4 — Scheduler Integration (P2) ⬜ Pending

**Goal:** Tasks are fully integrated with the chat-first model; scheduler is a background service, not a primary feature.

| # | Change | Files | Notes |
|---|--------|-------|-------|
| 18 | Add `Store.SaveScheduledTasks()` / `LoadScheduledTasks()` for crash recovery | `pkg/memory/store.go` | Persist task registry to `.norman/sessions/<id>/tasks.yaml`. |
| 19 | Connect scheduler `OutputHandler` callback to WS push events | `main.go`, `pkg/scheduler/scheduler.go` | Real-time progress updates during scheduled task execution. |
| 20 | Add `GET /health` with scheduler status (tasks registered, last tick time) | `main.go` | Health check includes background service health. |

### Phase 5 — OpenAPI Spec Cleanup (P2)

**Goal:** The generated API types match the chat-first model.

| # | Change | Files | Notes |
|---|--------|-------|-------|
| 21 | Regenerate `pkg/protocol/types.go` from updated OpenAPI spec | `internal/api/handlers.go` (oapi-codegen) | Remove manual task creation request types; add `SendMessageRequest`, `SessionStatus`. |
| 22 | Update `pkg/protocol/client.go` (generated HTTP client) | Same as above | Reflect new session-centric endpoints. |

## Priority Summary

| Phase | Scope | Effort | Status | Depends On |
|-------|-------|--------|--------|------------|
| P0 — Chat Workflow (CLI) | `norman chat` REPL, slash-commands, session switch/archive | ~3h | ✅ Done | None (pure CLI changes) |
| P1 — Session API | Add `/messages`, deprecate task writes, update spec | ~4-6h | ✅ Done | None (builds on current stub fixes) |
| P2 — Emergent Tasks | `Agent.CreateTask()`, `SendMessageWithTask`, GET/DELETE /api/tasks/{id} | ~3-4h | ✅ Done | Phase 1 complete |
| P3 — Chat CLI REPL | REPL mode (`norman chat`), slash-commands, in-chat `/history`, `/sessions`, `/switch`, `/archive` | ~3h | ✅ Done | Phase 0 complete (CLI infrastructure) |
| P3b — Session Mgmt | Transcript persistence (`SaveTranscript`/`LoadTranscript`), incremental turn tracking, `idleCleanupLoop` wiring | ~2h | ✅ Done | Phase 1 complete |
| P4 — Scheduler | Persistence callback, health endpoint | ~2h | ⬜ Pending | Phase 2 complete |
| P5 — Spec Cleanup | Regenerate types from new OpenAPI spec | ~1h | ⬜ Pending | Phase 1 complete |

**Total estimated effort:** ~18-20h (was ~13h; added ~5h for CLI REPL work)

## Notes

- All changes on branch `impl/stubs-real-implementations`.
- Build and tests pass: `go build ./cmd/norman-server/` and `go test ./...` succeed.
- No new dependencies planned; all changes use existing SDK types (`v1.ExecResult`).
- The spec rewrite (requirements.md) is already committed; code updates will align with the chat-first narrative.
