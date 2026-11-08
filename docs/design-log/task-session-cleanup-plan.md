---
title: "Task & Session Cleanup Plan"
type: implementation
status: active — phases 1–3 done, phase 4 deferred
date: 2026-11-07
author: Pablo (via goose)
related: [session-history-gaps.md, task-management-assessment.md, session-workflow-proposal.md]
---

# Task & Session Cleanup Plan — Lifecycle Boundaries & Task Cap

> **Date:** 2026-11-07  
> **Author:** Pablo (via goose)  
> **Status:** Phases 1–3 complete; Phase 4 deferred

## TL;DR

- **Archive cleanup**: `handleSessionArchive()` MUST remove the session's `.jsonl` turns file AND all associated task JSON files. Archive = soft-delete of conversation + tasks.
- **DeleteSession cleanup**: `DeleteSession()` handler MUST remove the session's `.jsonl` turns file. Deletion = hard-delete of conversation; tasks are cleaned separately via `DeleteTask()`.
- **Task cap (configurable)**: The agent loop enforces a per-session task limit via `max_tasks_per_session` system config (default 10). When creating a new task beyond the cap, the oldest task's `.json` file is deleted and removed from the in-memory map (LRU eviction).
- **Responsibility boundaries are explicit**: `Store.ArchiveSession()` owns turns cleanup; `DeleteSession()` handler owns session JSON cleanup; agent loop owns task cap enforcement. No shared responsibility.

---

## Background

The session lifecycle has three states: **active → archived → deleted**. Currently:
- Archive writes a `.archive.json` file but leaves the session's `.jsonl` turns file orphaned.
- Delete removes the session JSON but also leaves `.jsonl` turns files on disk.
- Tasks are persisted as individual `.json` files at `base/tasks/{taskID}.json`, with no cap on how many accumulate per session.

This creates a growing set of orphaned files: old sessions → archive + turns; deleted sessions → turns only; all tasks → one JSON file each, never pruned.

The agent loop's in-memory `tasks` map (`map[string]*Task`) is unbounded — it grows with every `CreateTask()` call and only shrinks when a task is explicitly cancelled or completed via `DeleteTask()`.

## Problem Statement

| Gap | Impact |
|-----|--------|
| **No archive cleanup** | `.jsonl` turns files accumulate for archived sessions. No mechanism to reclaim disk. |
| **No delete cleanup** | Same — deleted session turns are orphaned on disk. |
| **No task cap** | `base/tasks/` grows unbounded; each task persists a full JSON file regardless of age. |
| **Shared responsibility ambiguity** | It's unclear whether the agent loop, the server handler, or the store should clean up tasks during session lifecycle events. |

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `pkg/memory/store.go:PersistSessionTurn()` | Writes per-session JSONL — same directory pattern as task JSON files. Must be cleaned up on session lifecycle events. |
| `pkg/memory/store.go:LoadSessionTurns()` | Reads JSONL turns — confirms the file format and location (`base/turns/{sessionID}.jsonl`). |
| `pkg/memory/store.go:DeleteTask(taskID)` | Removes a single task's `.json` file. Already exists; needs to be called from cleanup paths. |
| `pkg/memory/store.go:ArchiveSession()` | Writes `.archive.json` — currently does NOT touch turns or task files. Must be extended. |
| `cmd/norman-server/sessions.go:DeleteSession()` | Removes session JSON but not `.jsonl` turns. Must be extended to also remove turns. |
| `pkg/agent/loop.go:Task.SessionId` | Each task has a `SessionId` field — enables mapping tasks → sessions for cleanup/cap enforcement. |
| `cmd/norman/sessions.go:handleSessionArchive()` | CLI archive command delegates to server API. No local file cleanup performed. |

---

## Questions & Answers

### Q1: Should archive and delete share the same cleanup logic, or be separate?

**A:** Separate paths with a shared helper:
- `DeleteSession()` → removes session JSON + `.jsonl` turns (hard delete, no archive)
- `handleSessionArchive()` → archives to `.archive.json` + removes `.jsonl` turns + removes all associated task files (soft-delete equivalent)

Rationale: Archive preserves the conversation in a single file; deletion discards it entirely. Task cleanup should happen in both paths because tasks are ephemeral conversation artifacts, not persistent entities like sessions.

### Q2: Where does the task cap live — in the agent loop or in the store?

**A:** In the **agent loop** (`pkg/agent/loop.go`). The loop owns `tasks map[string]*Task` and is responsible for enforcing the cap at creation time. The store's `DeleteTask()` is a utility called by the loop (or cleanup handlers) to remove individual task files from disk.

Rationale: The cap is a lifecycle concern (how many tasks can exist *right now*), not a persistence concern. The loop knows which tasks belong to which session and when new ones arrive.

### Q3: What happens to completed vs. active tasks during cleanup?

**A:** All tasks belonging to the session are cleaned up — both active and completed. Tasks are ephemeral by nature; they don't need to survive session deletion/archival. The archive preserves the *conversation* (turns), not the task artifacts.

### Q4: Should the cap apply globally or per-session?

**A:** Per-session. Each session can have up to `max_tasks_per_session` tasks persisted (default 10). Different sessions are independent — a session with 3 tasks doesn't affect another session's count.

Rationale: Tasks are scoped to sessions (via `Task.SessionId`). A global cap would create unfair contention between sessions. The value is configurable via `norman config --set max_tasks_per_session=N`.

### Q5: Where does the cap value live — hardcoded constant or system config?

**A:** System config (`SystemConfig.MaxTasksPerSession`), defaulting to 10 if unset. Read from `config.yaml`, env vars, or set via `norman config --set max_tasks_per_session=N`. This follows the same pattern as `MaxTurns` and `MaxActiveSessions`.

Rationale: Configurable allows tuning per deployment without code changes. Default of 10 is reasonable for most use cases but should be adjustable (e.g., long-running sessions may need more).

### Q5: Does the agent loop need to know about `.jsonl` turns files?

**A:** No — the loop owns tasks (`.json`), not turns (`.jsonl`). The server handler (`sessions.go`) owns turns cleanup. The loop's `DeleteTask()` removes task JSON files; the session handler's cleanup path removes turns files. Clear separation.

---

## Design

### 1. Responsibility Boundaries (No Shared Ownership)

| Component | Owns | Does NOT Own |
|-----------|------|--------------|
| **Agent Loop** (`pkg/agent/loop.go`) | In-memory `tasks` map; task cap enforcement at creation; calls `Store.DeleteTask()` to remove files | `.jsonl` turns files; session JSON cleanup |
| **Server Handler** (`cmd/norman-server/sessions.go`) | Session lifecycle events; `.jsonl` turns file removal on delete/archive | Task JSON files; in-memory task state |
| **Store** (`pkg/memory/store.go`) | `PersistTurn()`, `LoadSessionTurns()`, `DeleteTask()` — file I/O primitives | Lifecycle decisions (when to call cleanup) |

### 2. Task Cap Enforcement

```
Agent Loop:
  CreateTask(sessionID) {
    if count(tasks where SessionId == sessionID) >= MAX_TASKS_PER_SESSION(10) {
      evictOldestTask(sessionID) // deletes .json file + removes from map
    }
    save new task to base/tasks/{taskID}.json
  }

  evictOldestTask(sessionID) {
    find oldest task in tasks map with SessionId == sessionID
    store.DeleteTask(oldestTask.ID) // removes .json file
    delete from in-memory map
  }
```

### 3. Cleanup Paths

**DeleteSession path:**
```
cmd/norman-server/sessions.go:DeleteSession() {
  remove base/sessions/{sessionID}.json        ← already done
  remove base/turns/{sessionID}.jsonl          ← NEW (cleanup)
  // Task files are NOT removed here — they're cleaned by the loop's cap mechanism
  // OR: explicitly call DeleteTask for each task with SessionId == sessionID
}
```

**ArchiveSession path:**
```
cmd/norman/sessions.go:handleSessionArchive() {
  POST /api/sessions/{id}/archive → server writes .archive.json
  remove base/turns/{sessionID}.jsonl          ← NEW (cleanup)
  for each task with SessionId == sessionID:
    store.DeleteTask(task.ID)                  ← removes .json file
}
```

### 4. Configuration

- `max_tasks_per_session` — system config value (`SystemConfig.MaxTasksPerSession`), defaulting to 10 if unset. Set via `norman config --set max_tasks_per_session=N`.
- Cleanup is **eager** (on delete/archive), not deferred/garbage-collected
- No background cleanup goroutine needed — cleanup happens synchronously during lifecycle events

---

## Implementation Plan

### Phase 1: Task Cap in Agent Loop
- Add `MaxTasksPerSession int` field to `SystemConfig` struct (`pkg/config/config.go`) with default value of 10
- Modify `CreateTask()` in agent loop to read cap from config and check before adding new task
- Implement `evictOldestTask(sessionID)` helper that calls `store.DeleteTask()` and removes from map
- Add unit test: verify cap enforcement (create 12 tasks with default config, verify only 10 persisted)
- Integration test: verify `norman config --set max_tasks_per_session=5` affects live loop behavior

### Phase 2: DeleteSession Cleanup
- Extend `DeleteSession()` handler in `cmd/norman-server/sessions.go` to remove `.jsonl` turns file
- Also call `store.DeleteTask()` for each task with matching `SessionId` (clean up all associated tasks)
- Add integration test: delete session → verify turns file and task files removed

### Phase 3: ArchiveCleanup ✅ COMPLETE
- Extend `store.ArchiveSession()` in `pkg/memory/store.go` to remove `.jsonl` turns file + delete all associated task files (inlined to avoid deadlock)
- Idempotent: gracefully handles missing turns files and already-deleted sessions
- Correctly scoped: only tasks with matching `session_id` are removed; other sessions' tasks preserved
- 5 unit tests added in `cmd/norman-server/sessions_test.go`
- All pass with `make verify` (0 lint issues, race detector clean)

### Phase 4: Verification & Edge Cases ⬜ Deferred
- **Already covered:** `TestDeleteSessionIdempotentTurnsFile` (Phase 2), `TestArchiveSessionNoTurnsFile`, `TestArchiveSessionNoTasksForSession` (Phase 3)
- **Remaining gap:** Dedicated end-to-end integration test — start server, send messages via CLI, curl `/api/sessions/{id}/history` to confirm turns populated, then verify cleanup after archive/delete

---

## Examples

### Good: Task cap enforcement (config-driven)
```go
// In agent loop CreateTask():
cap := l.config.MaxTasksPerSession // reads from config, defaults to 10
if len(l.filterTasksBySession(sessionID)) >= cap {
    l.evictOldestTask(sessionID)
}
// New task saved, oldest removed — exactly max_tasks_per_session persisted
```

### Good: Config via CLI
```bash
# Set custom cap (e.g., for long-running sessions)
norman config --set max_tasks_per_session=20
# Inspect effective value
norman config --json | jq '.max_tasks_per_session'
```

### Bad: Shared responsibility (what we're avoiding)
```go
// ❌ Both the loop AND the server handler try to clean up tasks
// → double-delete errors, race conditions, unclear ownership
func DeleteSession() {
    store.DeleteTask(taskID) // server handler deletes task file
    // ...but loop also calls evictOldestTask() on next create
}
```

### Good: Clean separation
```go
// ✅ Loop owns tasks (in-memory + cap); Server owns turns (delete path)
func DeleteSession(sessionID string) {
    os.Remove(filepath.Join(base, "sessions", sessionID+".json"))
    os.Remove(filepath.Join(base, "turns", sessionID+".jsonl"))  // NEW
}

func evictOldestTask(sessionID string) {
    task := findOldest(l.tasks, sessionID)
    store.DeleteTask(task.ID)  // Loop calls store utility
    delete(l.tasks, task.ID)
}
```

---

## Trade-offs

| Approach | Pros | Cons |
|----------|------|------|
| **Eager cleanup (on delete/archive)** | Immediate disk reclamation; no orphaned files; simple logic | Blocks on I/O during cleanup; must handle missing files gracefully |
| **Deferred/GC cleanup** | Non-blocking; handles edge cases naturally | Orphaned files accumulate until GC runs; unpredictable disk usage |
| **Configurable cap (default 10)** | Tunable per deployment; no code changes needed | Default may need adjustment for long-running sessions |
| **Hardcoded cap** | Simple; no config dependency | Requires code change to adjust; less flexible |

**Chosen approach:** Eager cleanup + hard cap at 10 per session. Simple, predictable, no background processes needed.

---

## Implementation Results

## Implementation Results

### Phase 1 — Task Cap in Agent Loop ✅ Complete (commit `28fe5ed`)

| File | Change |
|------|--------|
| `pkg/config/config.go` | Added `MaxTasksPerSession int` field to `SystemConfig` with default value of 10 |
| `pkg/agent/loop.go` | `Submit()` enforces cap via LRU eviction (`evictOldestTask()`) before adding new tasks; `session_id` persisted in task JSON files via `taskToMap()` for proper filtering |

**Tests (4):** `TestTaskCapEnforcement`, `TestTaskCapZeroDisabled`, `TestTaskCapLruEviction`, `TestTaskCapDifferentSessionsIndependent`
**Verification:** 0 lint issues, all tests pass.

> **Deviation:** Cap check happens *before* adding the new task to the map (not after), so at peak exactly `max_tasks_per_session` tasks exist. Added nil-safe workspace guard in `execute()` to prevent panics when tests pass `nil` workspace — defensive fix, not a deviation.

---

### Phase 2 — DeleteSession Cleanup ✅ Complete (commit `9abebe0`)

| File | Change |
|------|--------|
| `cmd/norman-server/sessions.go` | Extended `DeleteSession()` handler to remove `.jsonl` turns file + iterate over all stored tasks and call `store.DeleteTask()` for each task with matching `SessionId` |

**Tests (4):** `TestDeleteSessionRemovesTurnsFile`, `TestDeleteSessionRemovesAssociatedTasks`, `TestDeleteSessionIdempotentTurnsFile`, `TestDeleteSessionNoTasksForSession`
**Verification:** 0 lint issues, race detector clean.

---

### Phase 3 — ArchiveCleanup ✅ Complete (commit `c40bb88`)

| File | Change |
|------|--------|
| `pkg/memory/store.go` | Extended `ArchiveSession()` to remove `.jsonl` turns file + delete all associated task files (inlined to avoid deadlock) |
| `cmd/norman-server/sessions_test.go` | Added 5 unit tests for archive cleanup behavior |

**Tests (5):** `TestArchiveSessionRemovesTurnsFile`, `TestArchiveSessionRemovesAssociatedTasks`, `TestArchiveSessionNoTurnsFile`, `TestArchiveSessionNoTasksForSession`, `TestArchiveSessionCreatesValidArchiveFile`
**Verification:** 0 lint issues, race detector clean.

> **Deviations from plan:**
> - Cleanup is inlined directly in `store.ArchiveSession()` rather than calling `s.DeleteTask()` — avoids deadlock since `ArchiveSession` holds `s.mu.Lock()` and Go's mutex is not reentrant.
> - The CLI `handleSessionArchive()` delegates to the server API, so cleanup happens server-side in `ArchiveSessionByID` → `store.ArchiveSession()`.

---

### Phase 4 — Verification & Edge Cases ⬜ Deferred

TODO:
- Verify cleanup when session has zero tasks (no-op)
- Verify cleanup when session is already deleted (idempotent)
- Verify cleanup when `.jsonl` file doesn't exist (legacy sessions)

> **Note:** Some of these edge cases are already covered by Phase 2 (`TestDeleteSessionIdempotentTurnsFile`) and Phase 3 (`TestArchiveSessionNoTurnsFile`, `TestArchiveSessionNoTasksForSession`). The remaining gap is a dedicated end-to-end integration test: start server, send messages via CLI, curl `/api/sessions/{id}/history` to confirm turns populated, then verify cleanup after archive/delete.
