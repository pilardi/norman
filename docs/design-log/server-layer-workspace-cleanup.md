# Server Layer Workspace Cleanup (Phase 5)

> This document captures the design rationale and implementation for replacing direct
> `WorkspaceManager` method calls in server HTTP handlers with the single `env.Workspace`
> interface abstraction.

---

## TL;DR

Server HTTP handlers (`sessions.go`) were calling `s.workspaceManager.GetSession()` and
`s.workspaceManager.UpdateLastActivity()` directly — bypassing the `Loop.Ws` abstraction
introduced in Phases 1–4. Phase 5 replaces these with `l.Ws.Get()` and `l.Ws.UpdateActivity()`,
adding two new interface methods (`UpdateActivity`, `Sessions`) to complete the server-layer
cleanup. After this phase, all session lifecycle operations flow through the single
`env.Workspace` interface.

---

## Prior Art

- **Phase 1–3**: Replaced three OpenShell references (`openShell`, `openShellClient`,
  `workspaceManager`) with a single `Ws env.Workspace` field on `Loop`.
- **Phase 4**: Added `GetSandboxName(ctx, sessionId string)` to the Workspace interface;
  exported `Loop.Ws` for server-layer access.
- **workspaces.go** (already Phase 4): `closeWorkspaceSessions()` uses `s.loop.Ws.GetSandboxName()`
  and `s.loop.Ws.Delete()`.
- **tasks.go**: `GetTask`/`TailTaskLog()` already use `s.loop.GetLogs()` via the loop.

The remaining direct `s.workspaceManager` calls were in `sessions.go`:
- `CreateSession()`: called `s.workspaceManager.GetSession(ctx, workspaceId, sid)` and
  `s.workspaceManager.UpdateLastActivity(workspaceId)`.

---

## Problem Statement

The server layer's `CreateSession()` handler still used `WorkspaceManager` methods directly:

```go
// Before (Phase 4):
_, err = s.workspaceManager.GetSession(c.Request().Context(), req.WorkspaceId, sid)
s.workspaceManager.UpdateLastActivity(req.WorkspaceId)
```

This violates the principle established in Phases 1–3: the agent loop should be the sole
abstraction for workspace/session operations. The server layer should not reach into
`WorkspaceManager` internals — it should use `l.Ws` methods.

Additionally, `GetSession()` on `WorkspaceManager` takes `(workspace, sessionId string)` and
creates a session if it doesn't exist (recoverOrCreate). The new `Get()` method on the
interface takes just `sessionId` and returns NotFound if non-existent — no auto-creation.
This is a semantic difference that must be reflected in the handler.

---

## Design Decisions (Frozen)

### 1. Add `UpdateActivity(ctx, sessionId string)` to Workspace interface

Replaces `UpdateLastActivity(workspace string)` with a session-ID-keyed variant:

```go
UpdateActivity(ctx context.Context, sessionId string)
```

**Rationale**: `UpdateLastActivity` on `WorkspaceManager` is keyed by workspace name
(string), but in the server handler we have a `sessionID`. The new method looks up the
session by ID and updates its `lastActivity` timestamp. This is semantically cleaner —
activity is per-session, not per-workspace-name.

### 2. Add `Sessions() map[string]*Session` to Workspace interface

Already implemented on `WorkspaceManager` (line 290 of workspace.go). Added to the
interface for completeness so server handlers can iterate sessions via `l.Ws.Sessions()`
instead of `s.workspaceManager.Sessions()`.

### 3. Keep `s.workspaces` map in server struct

The `s.workspaces` in-memory registry (workspace name → `*protocol.Workspace`) is a
different concern from session lifecycle. It's used for:
- Listing/filtering workspaces by status
- Tracking workspace metadata (purpose, scope, tags)
- Mapping session IDs to workspace names (for idle detection)

This is not replaced by the Workspace interface — it's a server-level registry.

### 4. `updateWorkspaceActivity()` and `checkWorkspaceIdle()` remain unchanged

These functions iterate `s.workspaces` to find workspace mappings for session IDs.
They are server-level concerns (workspace registry management), not session lifecycle.
They could be refactored later but are not part of Phase 5 scope.

---

## Q&A

**Q: Why not replace `s.workspaceManager.GetSession()` with `l.Ws.Get()` directly?**

A: `GetSession(workspace, sessionId)` creates a session if it doesn't exist
(recoverOrCreate). `Get(sessionId)` returns NotFound if non-existent. In `CreateSession()`,
we already just created the session via `createSessionInternal()`, so we need to *verify*
it exists in the workspace manager's cache — `Get()` is the right call (no auto-creation).

**Q: Why add `UpdateActivity` instead of reusing `UpdateLastActivity`?**

A: `UpdateLastActivity(workspace string)` takes a workspace name and updates the session
for that workspace. But in the handler, we have a `sessionID`. The new `UpdateActivity(sessionId)`
method is keyed by session ID directly, making it more precise.

**Q: Does this break any existing API contracts?**

A: No. `Get()` returning NotFound (vs nil + auto-create) is a breaking change from the old
`GetSession()` behavior, but in `CreateSession()` we already create the session first,
then verify — so the contract holds. The `Sessions()` map return type is stable.

**Q: What about `closeWorkspaceSessions()` in workspaces.go?**

A: Already uses `s.loop.Ws.GetSandboxName()` and `s.loop.Ws.Delete()` (Phase 4 change).
No changes needed here.

---

## Implementation Plan

| Step | File | Change |
|------|------|--------|
| 1 | `pkg/env/workspace_interface.go` | Add `UpdateActivity(ctx, sessionId string)` and `Sessions() map[string]*Session` to interface |
| 2 | `pkg/env/workspace.go` | Implement `UpdateActivity()` on WorkspaceManager (keyed by session ID) |
| 3 | `cmd/norman-server/sessions.go` | Replace `s.workspaceManager.GetSession(...)` → `l.Ws.Get(...)`, replace `s.workspaceManager.UpdateLastActivity(...)` → `l.Ws.UpdateActivity(...)` |
| 4 | `pkg/agent/loop_test.go` | Add `Sessions()` and `UpdateActivity()` to `FakeOpenShellClient` mock |
| 5 | Run `go test ./...` and `make lint` | Verify all tests pass and no lint issues |

---

## Post-Implementation Verification

- [x] `go build ./...` — compiles without errors
- [x] `go test ./...` — all tests pass (agent, env, agent/integration)
- [x] `make lint` — 0 issues
- [x] `git diff --stat` — 5 files changed, +55/-19 lines

---

*This completes Phase 5. The server layer now uses `l.Ws` exclusively for session lifecycle
operations. Phases 1–5 are complete.*
