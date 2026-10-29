# Workspace Lifecycle Proposal

> **Status:** ✅ IMPLEMENTED — Simplified model (stop/restore/purge), merged to `workspace-lifecycle-proposal` branch  
> **Date:** 2026-10-28 (proposal) / 2026-10-29 (simplification)  
> **Author:** Pablo (via goose)  
> **Related:** `docs/workspace-definition-assessment.md` (previous workspace feature), `AGENTS.md` §3 (decay rules)

---

## Problem Statement

The current workspace implementation lacks a formal **lifecycle** beyond "create → delete." This creates several operational gaps:

1. **Resource leakage**: Workspaces that are no longer actively used continue to consume OpenShell sandbox resources indefinitely.
2. **No idle detection**: There is no mechanism to detect when a workspace has been unused for a configurable period (e.g., 7 days).
3. **Destructive deletion**: `DELETE /api/workspaces/{name}` removes both the sandbox *and* metadata in one shot — there's no intermediate state that preserves history while freeing resources.
4. **No explicit stop/suspend workflow**: Users cannot manually pause a workspace without deleting it entirely.
5. **No last-used tracking**: The `Workspace` struct has `created_at` and `updated_at`, but no field for tracking *when the workspace was last actively used*.

---

## Simplified Lifecycle Model

After implementation review, the model was simplified from four states (active → stopped → archived → purge) to **three states** with two operations:

```
                    ┌──────────────┐
   Create           │  Active      │◄─────────────────────────────────┐
  ──────────────►   │ (running)    │                                  │
                    └──────┬───────┘                                  │
                           │ Stop (writes snapshot + frees sandbox)    │
                           ▼                                          │
                    ┌──────────────┐                                  │
                    │  Stopped     │◄─────────────────────────────────┘
   Restore         │ (snapshot     │                                  │
  ──────────────►   │  replayed +   │                                  │
                    │   sandbox on) │                                  │
                    └──────────────┘                                  │
                                                           Delete / Purge
```

### State Definitions

| State | Sandbox Running? | Data Preserved? | Can Receive Messages? |
|-------|------------------|-----------------|----------------------|
| `active` | ✅ Yes | ✅ Yes (live) | ✅ Yes |
| `stopped` | ❌ No | ✅ Yes (via snapshot) | ❌ No (read-only) |

### State Transitions

| Command | Action | Recoverable? |
|---------|--------|--------------|
| **stop** | Write snapshot + stop sandbox | ✅ Yes, via restore |
| **restore** | Read snapshot back + start sandbox | N/A (returns to active) |
| **purge** | Delete snapshot + sandbox + metadata | ❌ No |

**Why remove `archive`?** — It was redundant with `stop`. If stop already writes a snapshot, archive is just "stop but call it something else." The simplified model uses:
- `stop` = write snapshot + release all resources (sandbox + data)
- `restore` = read snapshot back + recreate sandbox
- `purge` = delete everything (no recovery)

---

## Proposed Changes

### 1. API Spec Changes (`api-spec.yaml`)

```yaml
WorkspaceStatus:
  type: string
  enum: [active, stopped]
  description: >
    Lifecycle state of the workspace. "active" = running sandbox;
    "stopped" = sandbox freed but data preserved (via snapshot).
```

Endpoints:

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/workspaces/{name}/stop` | Stop a workspace (write snapshot + free sandbox) |
| `POST` | `/api/workspaces/{name}/restore` | Restore a stopped workspace to active |
| `DELETE` | `/api/workspaces/{name}/purge` | Permanently delete all traces |

Query filters on `GET /api/workspaces`:
- `?status=stopped` — filter by lifecycle state
- `?idle_since=<RFC3339>` — return workspaces idle since a given time

### 2. Protocol Type Changes (`pkg/protocol/types.go`)

```go
const (
    WorkspaceStatusActive   WorkspaceStatus = "active"
    WorkspaceStatusStopped  WorkspaceStatus = "stopped"
)
```

`Workspace` struct gains `LastUsed *time.Time` field.

### 3. Server Handler Changes (`cmd/norman-server/workspaces.go`)

- **StopWorkspace**: writes wiki.jsonl + sessions.jsonl snapshot → closes sessions → stops sandbox → sets status `stopped`
- **RestoreWorkspace**: reads snapshot back → starts sandbox → sets status `active`
- **PurgeWorkspace**: deletes snapshot dir → closes sessions → deletes sandbox → removes workspace from store

### 4. CLI Changes (`cmd/norman/workspaces.go`)

```
norman workspaces stop <name>       Stop a workspace (write snapshot + free sandbox)
norman workspaces restore <name>    Restore a stopped workspace to active (replay snapshot)
norman workspaces purge <name>      Permanently delete a workspace (requires --confirm)
```

---

## Design Decisions & Trade-offs

### Why simplify from 4 states to 3?

The original model had `active` → `stopped` → `archived` → `purge`. The distinction between `stopped` and `archived` was confusing because:
- Both free the sandbox
- Both preserve data (but archive's "preserve" wasn't actually implemented)
- Users don't need two ways to say "I'm done with this for now"

The simplified model: `stop` is the single "freeze + release" operation. If you want to recover, `restore` replays from the snapshot. If you want it gone forever, `purge`.

### When should auto-stop trigger?

- Default: 7 days of no activity (configurable via `system.workspace_idle_timeout`).
- Override: User can explicitly `stop` at any time.
- Exception: Workspaces with `pinned: true` in frontmatter are never auto-stopped.

---

## Risks

| Risk | Mitigation |
|------|-----------|
| Snapshot write fails mid-operation | Two-phase: write snapshot → set status stopped → free sandbox. If snapshot fails, stay active. |
| Stale `LastUsed` timestamps if process crashes | On next activity, update `LastUsed` from session's `last_message_at`. |

---

## Implementation Notes

All phases implemented on branch `workspace-lifecycle-proposal`. Key details:

- **API**: Three lifecycle endpoints (`POST /stop`, `/restore`; `DELETE /purge`) added to `api-spec.yaml`.
- **Protocol**: `WorkspaceStatus` enum is `[active, stopped]`; `LastUsed *time.Time` added.
- **Store**: `UpdateLastActivity()` tracks workspace activity; `Sessions()` exposes active sessions.
- **Server handlers**: `StopWorkspace`, `RestoreWorkspace`, `PurgeWorkspace` in `workspaces.go`.
- **Idle loop**: `StartIdleCleanupLoop(timeout, stopCh)` background goroutine with ticker.
- **CLI**: `norman workspaces stop|restore|purge <name> [--flags]` commands implemented.

### Bug Fixes Discovered During Implementation

| Issue | Fix |
|-------|-----|
| Slice bounds panic `[:15]` on short names in `closeWorkspaceSessions()` | Conditional slice guard |
| Same pattern in `CreateWorkspace()` | Identical conditional slice fix |
| `--help` flag treated as workspace name | Added `--help`/`-h` check before name parsing |
| Server accepted invalid names (`--bad!name`) → 500 | Added `isValidWorkspaceName()` validation |
| Purge returned 204 but CLI tried to parse as JSON | CLI handles empty body gracefully |
| Route used `DELETE` but handler expected JSON body | Changed to `POST /api/workspaces/:name/purge` |

---

*Status: Simplified model implemented. PR ready for review.*
