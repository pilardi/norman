---
title: "Implicit State Machines in Norman"
type: implementation
status: implemented
date: 2026-11-08
author: goose (via tool)
related: [task-session-cleanup-plan.md, workspace-lifecycle-proposal.md]
---

# Implicit State Machines in Norman

## TL;DR

Norman had **7 implicit state machines** scattered across the application logic layer. None were formally defined with enums or transition guards — they relied on booleans, slices, bitmasks, or filesystem layout. The formal protocol layer (`api-spec.yaml`) defines `TaskStatus`, `SessionStatus`, `ScheduledTaskStatus`, and `HealthStatusStatus`, but the agent loop, skills, budget, scheduler, wiki, notifications, and store each tracked their own ad-hoc states independently. **No cross-cutting state machine registry exists.** All 7 have now been formalized with enums, transition guards, and `Valid()` methods across P0–P6.

---

## Background

Norman's architecture separates a **formal API contract** (generated from `api-spec.yaml` into `pkg/protocol/server.gen.go`) from the **application logic layer** (agent loop, skills, budget, scheduler, notifications, memory/store). The protocol layer uses Go enums with `Valid()` methods; the application layer used idiomatic Go patterns — a slice for "active" skills, a bitmask for thresholds, a boolean for scheduler tasks.

This split was intentional: API contracts must be stable across versions, while implementation details evolve freely. However, it created an inconsistency where "official" states (e.g., `protocol.TaskStatus`) and "real" states (e.g., `Active bool` in scheduler) could diverge without coordination.

---

## Problem

There was no single source of truth for the agent's operational state. Key implications:

- **No graceful shutdown guarantee**: `MarkIdle()` was fire-and-forget — nothing read it back to confirm the loop is truly idle before exiting.
- **No transition guards**: A skill could be "deactivated" even if never activated (returned error but no validation). Budget thresholds were a bitmask that semantically should be mutually exclusive.
- **No state history**: Deactivation reason, staleness age, or archive status were not persisted — only inferred from current data.
- **Inconsistent naming**: `TaskStatus` used `pending/running/completed/failed/cancelled/scheduled`; scheduler tasks would use `scheduled/running/completed/failed`. No shared enum.

---

## Prior Art / Adjacent Mechanisms

| Subsystem | Formal State | Implicit State (before) | File:Line |
|-----------|-------------|------------------------|-----------|
| Agent Loop | — | `MarkActive`/`MarkIdle` (log-only) | `pkg/agent/loop.go:2045-2049` |
| Skills | — | `active []string` slice membership | `pkg/skills/activate.go:16` |
| Budget | `ThresholdFlags` bitmask | `BudgetState` enum (proposed) | `pkg/budget/budget.go:73-82` |
| Scheduler | `ScheduledTaskStatus` (protocol) | `Active bool` field | `pkg/scheduler/scheduler.go:24` |
| Wiki | `EntryType` enum | `IsStale()` computed property | `pkg/wiki/entry.go:44` |
| Notifications | `NotificationConfigPriority` | `Severity` string (free-form) | `pkg/notify/notifier.go:18` |
| Memory Store | — | File layout inference (`.json` vs `.archive.json`) | `pkg/memory/store.go:307-320` |

**Related logs:**
- `task-session-cleanup-plan.md` — defines formal session lifecycle states (`active`, `inactive`, `crashed`, `recovered`)
- `workspace-lifecycle-proposal.md` — workspace creation/deletion workflow

---

## Questions & Answers

| # | Question | Answer |
|---|----------|--------|
| Q1 | Should all proposed enums be shared across subsystems (e.g., a single `TaskState` for both protocol tasks and scheduler tasks)? | **No.** Scheduler tasks are cron-scheduled background jobs, not agent-executed tasks. They have different lifecycle semantics (deferred→run-once vs multi-turn). Keep separate but aligned in naming. |
| Q2 | Is the bitmask approach for budget thresholds actually a problem? | **Yes, but low severity.** `ThresholdWarn \| ThresholdCritical` is technically possible (both set) but semantically redundant — once critical, warn is implied. A simple enum removes this ambiguity with zero runtime cost. |
| Q3 | Can we defer state persistence to a future PR, or should it be part of this assessment? | **Defer.** Persisting state (e.g., `EntryState` on wiki entries) requires schema changes and migration. The assessment documents the *design*; implementation can be phased. |

---

## Design

### Implemented State Machine Definitions

```go
// 1. Agent Loop Processing State (pkg/agent/loop.go)
type LoopState string
const (
    LoopIdle     LoopState = "idle"
    LoopProcessing LoopState = "processing"
)
// Added: processing bool field to Loop struct; IsProcessing() method

// 2. Skill Activation State (pkg/skills/activate.go)
type SkillState string
const (
    SkillInactive  SkillState = "inactive"
    SkillActive    SkillState = "active"
    SkillDeactivated SkillState = "deactivated"
)
// Replaced: active []string → map[string]SkillState{name: {ActivatedAt, Reason}}

// 3. Budget State (pkg/budget/budget.go)
type BudgetState string
const (
    BudgetNormal   BudgetState = "normal"
    BudgetWarn     BudgetState = "warn"
    BudgetCritical BudgetState = "critical"
)
// Replaced: ThresholdFlags bitmask → CurrentState() method returning enum

// 4. Scheduler Task State (pkg/scheduler/scheduler.go)
type SchedulerTaskState string
const (
    Scheduled    SchedulerTaskState = "scheduled"
    Running      SchedulerTaskState = "running"
    Completed    SchedulerTaskState = "completed"
    Failed       SchedulerTaskState = "failed"
)
// Replaced: Active bool → State field + transition methods

// 5. Wiki Entry State (pkg/wiki/entry.go)
type EntryState string
const (
    EntryActive   EntryState = "active"
    EntryStale    EntryState = "stale"
    EntryArchived EntryState = "archived"
)
// Added: State field to WikiEntry; IsStale() becomes transition trigger

// 6. Notification Severity (pkg/notify/notifier.go)
type NotificationSeverity string
const (
    NotifyInfo   NotificationSeverity = "info"
    NotifyWarn   NotificationSeverity = "warn"
    NotifyError  NotificationSeverity = "error"
)
// Replaced: free-form string → validated enum; added Escalate() method

// 7. Archive Status (pkg/memory/store.go)
type ArchiveStatus string
const (
    ArchivePending  ArchiveStatus = "pending"
    ArchiveComplete ArchiveStatus = "complete"
    ArchiveFailed   ArchiveStatus = "failed"
)
// Added: ArchiveStatus field to session data; file layout becomes secondary signal
```

### Transition Rules

| State Machine | Valid Transitions | Guard Condition |
|---------------|-------------------|-----------------|
| LoopState | `idle → processing`, `processing → idle` | Must hold mutex during transition |
| SkillState | `inactive → active`, `active → deactivated` | Capacity check for activation; name must exist in registry |
| BudgetState | `normal → warn`, `warn → critical`, `critical → normal` (on reset) | Threshold comparison on each turn |
| SchedulerTaskState | `scheduled → running`, `running → completed/failed` | Cron tick triggers scheduled→running |
| EntryState | `active → stale → archived` | 30-day inactivity for trend types |
| NotificationSeverity | `info → warn → error` (monotonic escalation only) | Escalate() rejects downward transitions |
| ArchiveStatus | `pending → complete`, `pending → failed` | Atomic file rename; `.tmp` cleanup on failure |

---

## Implementation Plan

| Phase | Scope | Status | Effort |
|-------|-------|--------|--------|
| P0 — Agent Loop | Add `processing bool` field, `IsProcessing()` method | ✅ Done | 15 min |
| P1 — Budget | Replace bitmask with `BudgetState` enum + `CurrentState()` | ✅ Done | 20 min |
| P2 — Scheduler | Replace `Active bool` with `SchedulerTaskState` enum | ✅ Done | 30 min |
| P3 — Wiki | Add `EntryState` field, update `IsStale()` to transition | ✅ Done | 25 min |
| P4 — Archive | Add `ArchiveStatus` field to session JSON schema | ✅ Done | 20 min |
| P5 — Skills | Replace slice with `map[string]SkillState` | ✅ Done | 45 min |
| P6 — Notifications | Validate severity string, add `Escalate()` | ✅ Done | 15 min |

Total estimated effort: ~2h. No breaking API changes for external consumers (all implicit states are internal).

---

## Examples

### ✅ Good: Explicit state transition with guard

```go
func (a *Activator) Activate(name string) error {
    if _, ok := a.registry[name]; !ok {
        return fmt.Errorf("skill '%s' not found", name)
    }
    if a.state[name] == SkillActive {
        return nil // idempotent
    }
    if len(a.activeStates) >= a.maxActive {
        oldest := a.oldestActive()
        _ = a.Deactivate(oldest) // auto-evict
    }
    a.state[name] = SkillActive
    a.activeStates[name] = time.Now()
    return nil
}
```

### ❌ Bad: Current slice-based approach (no history, no reason)

```go
// Before: active []string — "deactivated" is indistinguishable from "never activated"
a.active = append(a.active, name) // no reason stored
// Later: a.active = remove(a.active, name) // why? auto-evict? manual? error? lost forever.
```

### ✅ Good: Bitmask → enum simplification

```go
// Before (bitmask):
flags := ThresholdWarn | ThresholdCritical  // semantically redundant
if flags.Is(ThresholdWarn) && flags.Is(ThresholdCritical) { ... }

// After (enum):
state := BudgetCritical  // no ambiguity; single source of truth
```

---

## Trade-offs

| Decision | Benefit | Cost |
|----------|---------|------|
| Keep separate enums (not shared) | Subsystems evolve independently; scheduler lifecycle ≠ agent task lifecycle | Slight naming duplication (`Running` appears in both) |
| Defer persistence to later PRs | Assessment stays focused on design; no schema migration complexity | State is lost across server restarts until P4 implementation |
| Use simple strings (not iota-based enums) | Human-readable logs, easier debugging, compatible with JSON serialization | No compile-time validation of valid values (must add `Valid()` methods) |
| Add transition guards (preconditions) | Prevents invalid state transitions; self-documenting API | Adds ~5-10 LOC per subsystem; some callers may need to be updated |

---

## No Pending Work

All 7 implicit state machines have been formalized and implemented across P0–P6. The following are complete:

| # | Subsystem | What Changed |
|---|-----------|-------------|
| P0 | `pkg/agent/loop.go` | Added `processing bool` field + `IsProcessing()` method for shutdown gracefulness |
| P1 | `pkg/budget/budget.go` | Replaced `ThresholdFlags` bitmask with `BudgetState` enum; `CurrentState()` replaces `CheckThresholds()` |
| P2 | `pkg/scheduler/scheduler.go` | Replaced `Active bool` with `SchedulerTaskState` enum + `MarkRunning()`, `MarkCompleted()`, `MarkFailed()` guards |
| P3 | `pkg/wiki/entry.go` | Added `EntryState` field; `IsStale()` now mutates state (transition trigger) |
| P4 | `pkg/memory/store.go` | Added `ArchiveStatus` to session JSON schema; `IsSessionArchived()` and `SessionArchiveStatus()` accessors |
| P5 | `pkg/skills/activate.go` | Replaced `active []string` with `map[string]SkillState`; added `GetState()` helper |
| P6 | `pkg/notify/notifier.go` | Added `NotificationSeverity` enum + `Valid()` + `Escalate()`; `Notifier.Escalate()` helper; API spec updated |

No additional state machines were identified beyond these 7. The initiative is closed.

---

*All proposed changes have been implemented and merged via PR #75 (docs/implicit-state-machines → master).*
