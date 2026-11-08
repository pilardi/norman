---
title: "Task & Session Cleanup Plan"
type: implementation
status: draft
date: 2026-11-07
author: Pablo (via goose)
related: [session-history-gaps.md, task-management-assessment.md, session-workflow-proposal.md]
---

# Task & Session Cleanup Plan — Lifecycle Boundaries & Task Cap

> **Date:** 2026-11-07  
> **Author:** Pablo (via goose)  
> **Status:** Draft — awaiting review before implementation

## TL;DR

- **Archive cleanup**: `handleSessionArchive()` MUST remove the session's `.jsonl` turns file AND all associated task JSON files. Archive = soft-delete of conversation + tasks.
- **DeleteSession cleanup**: `DeleteSession()` handler MUST remove the session's `.jsonl` turns file. Deletion = hard-delete of conversation; tasks are cleaned separately via `DeleteTask()`.
- **Task cap (last 10)**: The agent loop enforces a per-session task limit of 10 persisted tasks. When creating a new task beyond the cap, the oldest task's `.json` file is deleted and removed from the in-memory map (LRU eviction).
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

**A:** Per-session. Each session can have up to 10 tasks persisted. Different sessions are independent — a session with 3 tasks doesn't affect another session's count.

Rationale: Tasks are scoped to sessions (via `Task.SessionId`). A global cap would create unfair contention between sessions.

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

### 4. Constants & Configuration

- `MAX_TASKS_PER_SESSION = 10` — hardcoded constant in agent loop config or as a package-level const
- Cleanup is **eager** (on delete/archive), not deferred/garbage-collected
- No background cleanup goroutine needed — cleanup happens synchronously during lifecycle events

---

## Implementation Plan

### Phase 1: Task Cap in Agent Loop
- Add `MAX_TASKS_PER_SESSION = 10` constant to `pkg/agent/loop.go`
- Modify `CreateTask()` to check cap before adding new task
- Implement `evictOldestTask(sessionID)` helper that calls `store.DeleteTask()` and removes from map
- Add unit test: verify cap enforcement (create 12 tasks, verify only 10 persisted)

### Phase 2: DeleteSession Cleanup
- Extend `DeleteSession()` handler in `cmd/norman-server/sessions.go` to remove `.jsonl` turns file
- Also call `store.DeleteTask()` for each task with matching `SessionId` (clean up all associated tasks)
- Add integration test: delete session → verify turns file and task files removed

### Phase 3: ArchiveCleanup
- Extend `handleSessionArchive()` in `cmd/norman/sessions.go` to remove `.jsonl` turns file
- Iterate over session's tasks and call `store.DeleteTask()` for each
- Add integration test: archive session → verify turns file removed, task files removed

### Phase 4: Verification & Edge Cases
- Verify cleanup when session has zero tasks (no-op)
- Verify cleanup when session is already deleted (idempotent)
- Verify cleanup when `.jsonl` file doesn't exist (legacy sessions)

---

## Examples

### Good: Task cap enforcement
```go
// In agent loop CreateTask():
if len(l.filterTasksBySession(sessionID)) >= 10 {
    l.evictOldestTask(sessionID)
}
// New task saved, oldest removed — exactly 10 persisted
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
| **Cap at 10 tasks per session** | Predictable disk usage; bounded memory footprint | Oldest tasks lost permanently; no "keep all" option |
| **Cap at 10 + keep completed tasks** | Retains historical task state | Defeats the purpose of a cap; defeats LRU eviction model |

**Chosen approach:** Eager cleanup + hard cap at 10 per session. Simple, predictable, no background processes needed.

---

## Implementation Results

<!-- Filled in during/after implementation -->
<!-- Status: pending | deviations documented below -->
