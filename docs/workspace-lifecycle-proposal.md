# Workspace Lifecycle Proposal

> **Status:** ✅ IMPLEMENTED — All phases complete, merged to `workspace-lifecycle-proposal` branch  
> **Date:** 2026-10-28 (proposal) / 2026-10-28 (implementation)  
> **Author:** Pablo (via goose)  
> **Related:** `docs/workspace-definition-assessment.md` (previous workspace feature), `AGENTS.md` §3 (decay rules)

---

## Problem Statement

The current workspace implementation lacks a formal **lifecycle** beyond "create → delete." This creates several operational gaps:

1. **Resource leakage**: Workspaces that are no longer actively used continue to consume OpenShell sandbox resources indefinitely.
2. **No idle detection**: There is no mechanism to detect when a workspace has been unused for a configurable period (e.g., 7 days).
3. **Destructive deletion**: `DELETE /api/workspaces/{name}` removes both the sandbox *and* metadata in one shot — there's no intermediate "archived" state that preserves history while freeing resources.
4. **No explicit stop/suspend workflow**: Users cannot manually pause a workspace without deleting it entirely.
5. **No last-used tracking**: The `Workspace` struct has `created_at` and `updated_at`, but no field for tracking *when the workspace was last actively used* (session start, message sent, task created).
6. **Coupled lifecycle**: Sessions are ephemeral; workspaces should outlive sessions but can be "stopped" without data loss.

---

## Current State Analysis

| Layer | Current Behavior | Gap |
|-------|-----------------|-----|
| **API** (`api-spec.yaml`) | `status: [active, archived]` enum exists but is never used | No transitions between states; `archived` is a dead branch |
| **Server** (`cmd/norman-server/workspaces.go`) | `Create`, `Read`, `Update`, `Delete` — no `Stop`, `Restore`, `Archive` | Lifecycle is binary: active ↔ gone |
| **Protocol** (`pkg/protocol/types.go`) | `WorkspaceStatus = "active" | "archived"` | No `stopped` or `idle` state |
| **Memory Store** (`pkg/memory/store.go`) | Saves/loads workspace JSON with `status` field | No last-used timestamp; no auto-pruning |
| **Env Manager** (`pkg/env/workspace.go`) | `CloseAll()` shuts down all sessions but doesn't track workspace-level inactivity | No per-workspace idle timeout |
| **Sessions** (`cmd/norman-server/sessions.go`) | Sessions are bound to workspaces but don't report back "workspace is idle" | No feedback loop for workspace lifecycle decisions |

---

## Proposed Lifecycle States

```
                    ┌──────────────┐
   Create           │  Active      │◄─────────────────────────────────┐
  ──────────────►   │ (running)    │                                  │
                    └──────┬───────┘                                  │
                           │ Stop / Idle timeout                     │
                           ▼                                          │
                    ┌──────────────┐                                  │
                    │  Stopped     │◄─────────────────────────────────┘
   Archive          │ (sandbox free│                                  │
  ──────────────►   │  but data kept)                                 │
                    └──────┬───────┘                                  │
                           │ Restore                                   │
                           ▼                                          │
                    ┌──────────────┐                                  │
                    │  Archived    │                                  │
                    │ (read-only,   │                                  │
                    │  no sandbox)  │                                  │
                    └──────────────┘                                  │
                                                           Delete / Purge
```

### State Definitions

| State | Sandbox Running? | Data Preserved? | Can Receive Messages? | Auto-Pruned? |
|-------|------------------|-----------------|----------------------|--------------|
| `active` | ✅ Yes | ✅ Yes | ✅ Yes | No |
| `stopped` | ❌ No | ✅ Yes | ❌ No (read-only) | Configurable timeout |
| `archived` | ❌ No | ✅ Yes (immutable) | ❌ No | Never (until explicit purge) |

---

## Proposed Changes

### 1. API Spec Changes (`api-spec.yaml`)

Add new lifecycle transitions and expand the status enum:

```yaml
WorkspaceStatus:
  type: string
  enum: [active, stopped, archived]
  description: >
    Lifecycle state of the workspace. "active" = running sandbox;
    "stopped" = sandbox freed but data preserved; "archived" = immutable read-only snapshot.
```

Add new endpoints:

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/workspaces/{name}/stop` | Stop the workspace (free sandbox, keep metadata) |
| `POST` | `/api/workspaces/{name}/restore` | Restore a stopped/archived workspace to active |
| `POST` | `/api/workspaces/{name}/archive` | Archive a workspace (make read-only snapshot) |
| `DELETE` | `/api/workspaces/{name}/purge` | Permanently delete all traces (sandbox + metadata) |

Add optional query parameter to `GET /api/workspaces`:
- `?status=stopped` — filter by lifecycle state
- `?idle_since=<RFC3339>` — return workspaces idle since a given time

### 2. Protocol Type Changes (`pkg/protocol/types.go`)

Update the `Workspace` struct:

```go
type Workspace struct {
    Id        *openapi_types.UUID `json:"id,omitempty"`
    Name      *string             `json:"name,omitempty"`
    Path      *string             `json:"path,omitempty"`
    Purpose   *string             `json:"purpose,omitempty"`
    Scope     *[]string           `json:"scope,omitempty"`
    Tags      *[]string           `json:"tags,omitempty"`
    CreatedAt *time.Time          `json:"created_at,omitempty"`
    UpdatedAt *time.Time          `json:"updated_at,omitempty"`
    LastUsed  *time.Time          `json:"last_used,omitempty"`   // NEW
    Status    *WorkspaceStatus    `json:"status,omitempty"`
}
```

Add new constants:
```go
const (
    WorkspaceStatusActive   WorkspaceStatus = "active"
    WorkspaceStatusStopped  WorkspaceStatus = "stopped"
    WorkspaceStatusArchived WorkspaceStatus = "archived"
)
```

### 3. Store Changes (`pkg/memory/store.go`)

- Add `LastUsed` field to workspace JSON persistence
- Add `PruneIdleWorkspaces(maxAge time.Duration, status WorkspaceStatus)` method for auto-pruning stopped workspaces
- Add `ListByStatus(status WorkspaceStatus)` filter method

### 4. Server Handler Changes (`cmd/norman-server/workspaces.go`)

New handler methods:

```go
func (s *server) StopWorkspace(c echo.Context, name string) error
func (s *server) RestoreWorkspace(c echo.Context, name string) error
func (s *server) ArchiveWorkspace(c echo.Context, name string) error
func (s *server) PurgeWorkspace(c echo.Context, name string) error
```

Logic for each:
- **Stop**: Call `session.Delete()` on the sandbox, set status to `stopped`, broadcast event.
- **Restore**: Recreate the sandbox via `CreateWorkspace()`, set status to `active`, update `LastUsed`.
- **Archive**: Create a snapshot of workspace data (markdown + wiki), mark as `archived`, free sandbox.
- **Purge**: Archive first (for safety), then delete from store and remove all traces.

### 5. Env Manager Changes (`pkg/env/workspace.go`)

- Add `LastUsed` tracking: update timestamp on every session creation, message send, or task creation within the workspace.
- Add `GetIdleSince(workspace string) *time.Time` — returns when the workspace was last active, or nil if never used.
- Add `IsIdle(workspace string, threshold time.Duration) bool` — convenience check for auto-stopping logic.

### 6. Session Integration Changes (`cmd/norman-server/sessions.go`)

- Update `CreateSession` and `SendMessage` to update the workspace's `LastUsed` timestamp.
- Add a `PostMessageHook` or callback that notifies the workspace manager of activity.

### 7. Agent Loop Changes (`pkg/agent/loop.go`)

- When the agent loop starts a session, mark the workspace as "in use."
- When all sessions for a workspace are closed, mark it as "idle" (start idle timer).

---

## Implementation Plan

| Phase | Scope | Effort | Priority |
|-------|--------|--------|----------|
| **P1** | API spec + protocol types (`stop`, `restore`, `archive`, `purge` endpoints, `LastUsed` field) | Medium | High — foundation for all other changes |
| **P2** | Server handlers implementing the new lifecycle transitions | Medium | High — user-facing behavior |
| **P3** | Env manager idle tracking + auto-stop mechanism | Medium | Medium — operational automation |
| **P4** | Session integration (update `LastUsed` on activity) | Low | Medium — closes the feedback loop |
| **P5** | Agent loop "in use" / "idle" signaling | Low | Low — completes the lifecycle |
| **P6** | CLI commands (`norman workspace stop/restore/archive/purge`) | Medium | Longer-term |

---

## Design Decisions & Trade-offs

### Why `stopped` vs. `archived` as separate states?

- **Stopped**: Sandbox is freed but can be *instantly* restored with full fidelity (all files, wiki state). Ideal for temporary pauses ("I'm done for the day").
- **Archived**: Data is snapshotted immutably; sandbox is permanently freed. Ideal for "this project is complete, keep it around for reference."

### When should auto-stop trigger?

- Default: 7 days of no activity (configurable via `system.workspace_idle_timeout`).
- Override: User can explicitly `stop` at any time.
- Exception: Workspaces with `pinned: true` in frontmatter are never auto-stopped.

### What happens to sessions during a stop?

- Active sessions are gracefully closed (broadcast `session_event` to connected clients).
- Session transcripts and turns are preserved in the store.
- New messages to a stopped workspace return a "workspace is stopped" response with a restore prompt.

### Is archive destructive to the sandbox?

- No. Archive creates a markdown/JSON snapshot of all wiki entries, sessions, and tasks *before* freeing the sandbox. The original sandbox files are preserved until purge.

---

## Risks

| Risk | Mitigation |
|------|-----------|
| Data loss during archive if snapshot fails | Two-phase: write snapshot → mark archived → free sandbox. If snapshot fails, stay active. |
| Stale `LastUsed` timestamps if process crashes | On next `GetSession`/`CreateWorkspace`, update `LastUsed` from the session's `last_message_at`. |
| API breaking changes for existing clients | New states are additive to the enum; existing `active` behavior is unchanged. |
| Performance impact of frequent `LastUsed` updates | Batch updates: only flush to disk every N messages or on session close. |

---

## Migration Path

For existing workspaces already using `status: "archived"` (from the previous assessment):

1. On server startup, treat all `"archived"` entries as `"stopped"` during migration.
2. Run a one-time migration script that populates `LastUsed` from `updated_at`.
3. After v1 release, `"archived"` can be reintroduced as a distinct final state (read-only snapshot).

---

## Open Questions (Resolved)

| # | Question | Resolution |
|---|----------|------------|
| 1 | Should `archive` also freeze the wiki? | Implemented: archive writes an immutable markdown/JSON snapshot; new entries can still be added. Freeze is deferred. |
| 2 | Should there be a `soft-delete`? | Deferred — not implemented in P1–P6. |
| 3 | Default idle timeout? | Configurable via `StartIdleCleanupLoop(timeout, stopCh)` with default 7 days. |
| 4 | `purge` confirmation? | Implemented: CLI requires `--confirm`; server validates `confirm: true` in request body. |

---

## Implementation Notes

All six phases (P1–P6) are implemented and committed on branch `workspace-lifecycle-proposal`. Key implementation details:

- **API**: Four lifecycle endpoints (`POST /stop`, `/restore`, `/archive`; `DELETE /purge`) added to `api-spec.yaml` and regenerated via oapi-codegen v2.8.1.
- **Protocol**: `WorkspaceStatus` enum expanded to `[active, stopped, archived]`; `LastUsed *time.Time` added to `Workspace` struct.
- **Store**: `UpdateLastActivity()` tracks workspace activity; `Sessions()` public accessor exposes active sessions for idle detection.
- **Server handlers**: `StopWorkspace`, `RestoreWorkspace`, `ArchiveWorkspace`, `PurgeWorkspace` implemented in `workspaces.go`.
- **Idle loop**: `StartIdleCleanupLoop(timeout, stopCh)` background goroutine with ticker; auto-stops workspaces after configurable timeout.
- **Agent loop**: `MarkActive(workspace)` / `MarkIdle(workspace)` provide in-use/idle feedback without requiring Client-level methods.
- **CLI**: `norman workspaces stop|restore|archive|purge <name> [--flags]` commands implemented with proper help text and validation.

### Bug Fixes Discovered During Implementation

| Issue | Fix |
|-------|-----|
| Slice bounds panic `[:15]` on short workspace names in `closeWorkspaceSessions()` | Conditional slice guard: `if len(cleanName) > 15 { cleanName = cleanName[:15] }` |
| Same pattern in `CreateWorkspace()` (line 107 of `pkg/env/workspace.go`) | Identical conditional slice fix applied |
| `--help` flag treated as workspace name in CLI create | Added `--help`/`-h` check before name parsing |
| Server accepted invalid names (`--bad!name`) → 500 | Added `isValidWorkspaceName()` validation (alphanumeric + `_`/`-`) |
| Purge returned 204 No Content but CLI tried to parse as JSON | CLI now handles empty body gracefully |
| Route used `DELETE` but handler expected JSON body via `Bind()` | Changed to `POST /api/workspaces/:name/purge` |

---

*Status: All phases implemented. PR ready for review.*
