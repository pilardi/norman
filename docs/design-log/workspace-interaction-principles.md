---
title: "Agent Loop ↔ Workspace Interaction Principles"
type: assessment
status: draft
date: 2026-11-06
author: Pablo (via goose)
related: [openshell-interaction.md, session-workflow-proposal.md, workspace-lifecycle-proposal.md, workspace-definition-assessment.md]
---

# Agent Loop ↔ Workspace Interaction Principles — Assessment & Reorganization Plan

> **Status:** 📝 Draft — Awaiting implementation approval  
> **Date:** 2026-11-06  
> **Author:** Pablo (via goose)  
> **Related:** `./openshell-interaction.md`, `./session-workflow-proposal.md`, `./workspace-lifecycle-proposal.md`

## Decisions for the Implementer (TL;DR)

- **Single dependency**: The agent loop should depend on exactly **one** abstraction (`env.Workspace`) — not three separate OpenShell references.
- **Non-creating `Get()`**: `GetSession()` must return a `NotFound` error if the workspace doesn't exist; auto-creation is removed. Creation happens only via explicit `Create()`.
- **Sandbox encapsulated**: `*v1.Sandbox` and raw SDK types stay inside `pkg/env/`. No public method outside env should return a raw sandbox object or expose OpenShell internals.
- **`Delete()` on Workspace, not Session**: Session lifecycle management (status, waitReady) is internal to the workspace; `Delete()` belongs on the `Workspace` interface.
- **No task auto-creates workspaces**: Tasks submitted via `loop.Submit()` must use an *existing* session or fail with a clear error — never silently create one.

These decisions are frozen once implementation begins. Deviations must be documented in the "Implementation Results" section below.

---

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `pkg/env/sdk_adapter.go` — `SDKAdapter` wrapping `*Client` → adapts to `OpenShellClient` interface | Solves: provides the interface the loop currently depends on. Constrains: it exposes raw SDK operations (`CreateSandbox`, `WaitReady`, `GetLogs`) that leak OpenShell internals. |
| `pkg/env/client.go` — `Client` wrapping OpenShell Go SDK | Solves: single concrete client for real operations. Constrains: its methods are public and used directly by server handlers (`workspaces.go`, `sessions.go`). |
| `pkg/env/workspace.go` — `WorkspaceManager` managing workspace↔sandbox mappings | Solves: handles sandbox name resolution, session caching. Constrains: has 15+ public methods exposing OpenShell lifecycle (`CloseSandbox`, `StopSandbox`, `StartSandbox`) outside env. |
| `pkg/env/session.go` — `Session` struct wrapping a sandbox | Solves: provides `Exec()`, `ReadFile()`, `WriteFile()` for agent tool calls. Constrains: exposes `Sandbox()` (raw SDK-assigned name), `Status()`, `IsReady()`, `WaitReady()`, `Delete()` — all public methods leaking internal state. |
| `pkg/agent/loop.go` — Core agent loop | Solves: task execution, skill activation, context building. Constrains: currently holds three OpenShell references (`openShell`, `openShellClient`, `workspaceManager`) and imports `openshell/v1` directly for `*v1.Sandbox`. |
| `cmd/norman-server/sessions.go` — Session creation handler | Solves: HTTP endpoint for session creation. Constrains: calls `workspaceManager.GetSession()` which auto-creates sandboxes (shortcut violating principle #3). |
| `cmd/norman-server/workspaces.go` — Workspace CRUD handlers | Solves: canonical entry point for workspace creation. Constrains: creates sandboxes via `workspaceManager.CreateWorkspace()` directly — a second path to sandbox creation alongside the agent loop. |
| `./openshell-interaction.md` (implemented 2026-10-18) | Solves: documents OpenShell boundary model. Constrains: confirms agent should not see credentials or direct SDK calls; all tool I/O goes through session layer. |
| `./workspace-lifecycle-proposal.md` (implemented 2026-10-29) | Solves: defines `active`/`stopped` states with snapshot-based recovery. Constrains: workspace creation/deletion must respect these states — no creating a sandbox for a "purged" workspace. |
| `./workspace-definition-assessment.md` (implemented 2026-10-15) | Solves: defines workspace identity (name, purpose, scope, tags). Constrains: `WorkspaceDefinition.SandboxName` field is public — leaks OpenShell naming convention into env's API surface. |

---

## Problem Statement

The current codebase has **7 concrete violations** of the stated project principles about how the agent loop, workspace, and OpenShell should interact:

| # | Principle | Current Violation | File:Line |
|---|-----------|------------------|-----------|
| 1 | Agent loop must not directly interact with OpenShell nor know it exists | Loop holds `openShell` (interface), `openShellClient` (`*env.Client` concrete), and `workspaceManager` (`*env.WorkspaceManager`) — **three** separate OpenShell references. Imports `openshell/v1` for `*v1.Sandbox`. | `loop.go:50,58,62` |
| 2 | Workspace is only created as a result of user request | `GetSession()` auto-creates sandboxes when one doesn't exist (shortcut). Tasks can effectively create workspaces without explicit user request. | `loop.go:173` |
| 3 | Agent loop is not allowed to create a workspace; if attached workspace doesn't exist, it should fail the task | `executeWithLLM()` calls `l.openShell.CreateSandbox()` directly inside the loop. The loop auto-creates sandboxes on first use. | `loop.go:624` |
| 4 | A task cannot create a new workspace — only user-requested action | `GetSession()` is called from both server (`sessions.go`) and agent loop (`loop.go`). Tasks via `loop.Submit()` → `executeWithLLM()` → `GetSession()` implicitly create workspaces. | `loop.go:160` |
| 5 | From the agent's perspective, it must not know anything about OpenShell — that is an implementation detail of the workspace | Loop imports `github.com/NVIDIA/OpenShell/sdk/go/openshell/v1` directly for `*v1.Sandbox`, `*v1.TLSConfig`. Calls `activator.DeployToSandbox("roger", session)` referencing sandbox internals. | `loop.go:13,648` |
| 6 | The agent loop has two OpenShell references; it should only have a client to interact with the workspace through a session | Loop holds `openShell OpenShellClient` (interface) AND `openShellClient *env.Client` (concrete SDK client). `NewWithClient()` explicitly adds the second. | `loop.go:50,58` |
| 7 | No public `Sandbox()` operations should be exposed outside `pkg/env/` | `Session.Sandbox()` returns raw SDK-assigned name publicly. `WorkspaceManager.SandboxName()` is public. `Session.Status()`, `Session.IsReady()`, `Session.WaitReady()`, `Session.Delete()` are all public methods exposing sandbox internals. `WorkspaceDefinition.SandboxName` field is public. | `session.go:44,74,88,93`; `workspace.go:18,32` |

### Two Paths to Sandbox Creation (No Single Source of Truth)

```
┌──────────────────────────────┐     ┌──────────────────────────────────┐
│  cmd/norman-server/workspaces │     │  pkg/agent/loop.GetSession()    │
│  CreateWorkspace() handler    │     │  (auto-creates on first use)     │
│  → workspaceManager.Create()  │     │  → client.GetSandbox()         │
│  → client.CreateSandbox()     │     │  → if nil: client.CreateSandbox│
└──────────────────────────────┘     └──────────────────────────────────┘
```

Both paths call `client.CreateSandbox()` directly — no single authoritative "does this workspace exist?" check. The server handler is the *canonical* path; the agent loop's auto-creation is a *shortcut*.

---

## Design: Proposed Architecture

### 1. New `Workspace` Interface (in `pkg/env/`)

```go
// Workspace encapsulates all sandbox lifecycle operations behind a session-centric API.
// It is the single dependency the agent loop depends on — no direct OpenShell knowledge.
type Workspace interface {
    // Create creates a new workspace-backed session. Returns an error if creation fails.
    // This is the ONLY path for creating new workspaces (user-requested action).
    Create(ctx context.Context, sessionId string) (*Session, error)

    // Get retrieves an existing session by ID. Returns NotFound error if non-existent.
    // Does NOT auto-create — caller must explicitly request creation via Create().
    Get(ctx context.Context, sessionId string) (*Session, error)

    // Exists checks whether a workspace has been created without side effects.
    Exists(ctx context.Context, sessionId string) bool

    // Delete removes a workspace and its associated sandbox.
    Delete(ctx context.Context, sessionId string) error

    // Session returns the current session state (read-only view).
    Session() *Session
}
```

**Design rationale:** `Get()` is non-creating — it fails if the session doesn't exist. This enforces principle #3. `Create()` is the sole path for new workspace creation. No shortcuts.

### 2. Simplified Agent Loop Dependencies

```go
// Before (current):
type Loop struct {
    openShell        OpenShellClient       // interface
    openShellClient  *env.Client            // concrete SDK client
    workspaceManager *env.WorkspaceManager  // workspace-to-sandbox mapping
}

// After:
type Loop struct {
    workspace env.Workspace   // single dependency — replaces all three above
}
```

### 3. Session API Surface (Reduced)

| Method | Before | After | Rationale |
|--------|--------|-------|-----------|
| `SessionID()` | ✅ public | ✅ public | Identity — keep |
| `LastActivity()` | ✅ public | ✅ public | Read-only state — keep |
| `Exec()` | ✅ public | ✅ public | Tool dispatch — keep |
| `ReadFile()` | ✅ public | ✅ public | File I/O — keep |
| `WriteFile()` | ✅ public | ✅ public | File I/O — keep |
| `Sandbox()` | ❌ returns `string` (raw SDK name) | removed | Leaks OpenShell naming convention |
| `Status(ctx)` | ❌ returns `*v1.Sandbox` | → `SessionStatus` enum | Returns raw SDK type; replace with simple enum |
| `IsReady(ctx)` | ❌ public | removed | Internal concern — move to Workspace |
| `WaitReady(ctx)` | ❌ returns `*v1.Sandbox` | removed | Exposes raw SDK type; move to Workspace |
| `Delete(ctx)` | ❌ public on Session | → `Workspace.Delete()` | Lifecycle belongs on the workspace, not the session |

### 4. Flow After Reorganization

```
User: norman sessions create --workspace my-project
    │
    ▼
┌──────────────────────────┐     ┌─────────────────────┐
│  cmd/norman-server       │     │  pkg/env/Workspace  │
│  CreateSession() handler │────▶│  .Create(sessionId) │
│  → workspaceManager.Create│     │    (single path)    │
└──────────────────────────┘     └─────────────────────┘

Agent loop: loop.Submit("Explain this", "default", "session-1")
    │
    ▼
┌──────────────────────────┐     ┌─────────────────────┐
│  pkg/agent/loop.go       │     │  workspace.Get(id)   │
│  executeWithLLM()        │────▶│  (returns error if   │
│  → GetSession()          │     │   workspace doesn't  │
│  → RunCommand / ReadFile │     │   exist — no auto-   │
│  → WriteFile             │     │   creation)          │
└──────────────────────────┘     └─────────────────────┘
```

**Key change:** `GetSession()` becomes a simple delegation to `workspace.Get()`. If the workspace doesn't exist, it returns `NotFound` — the task fails cleanly rather than auto-creating.

### 5. Bootstrap Wiring (Simplified)

```go
// Before:
loopClient := env.NewSDKAdapter(openShellClient)
wsManager := env.NewWorkspaceManager(openShellClient, serverURL)
loop := agent.NewWithClient(cfg, store, rawSources, activator, wsHub, loopClient, openShellClient).WithWorkspaceManager(wsManager)

// After (single dependency):
ws := env.NewWorkspaceManager(openShellClient, serverURL)  // implements Workspace interface
loop := agent.New(cfg, store, rawSources, activator, wsHub, ws)  // New() takes env.Workspace
```

---

## Questions & Answers

### Q1: Should `Workspace.Create()` accept a workspace ID or sessionId?

**A:** `sessionId` — the session IS the workspace identity. Workspace ID is derived from session (via `SandboxName()` convention). This avoids duplication of identity concepts. The caller passes the session UUID; the workspace manager maps it to the correct sandbox.

### Q2: Does `Exists()` need to check SDK state or just local cache?

**A:** Local cache + lightweight SDK ping (`GetSandbox` without creating). This gives an O(1) existence check without side effects (no new sandbox allocation, no deployment). The actual creation path (`Create()`) remains the single canonical entry point.

### Q3: Should `Session.Status()` return a simplified status enum instead of `*v1.Sandbox`?

**A:** ✅ Yes — replace with `func (s *Session) Status() SessionStatus` (enum: `Ready`, `Running`, `Stopped`, `Error`). This removes the raw SDK type leak while preserving the information the agent loop needs.

### Q4: Where does `activator.DeployToSandbox("roger", session)` belong?

**A:** Move to `Workspace.Create()` as part of workspace initialization, not an agent loop concern. The agent loop should only know how to *use* a session (exec, read, write), not how it was provisioned. This is the skill deployment responsibility of the workspace layer.

### Q5: What about `GetLogs(sandboxID string)` called from `tasks.go`?

**A:** Route through `workspace.GetLogs(sessionId)` — the workspace knows which sandbox ID maps to which session. The agent loop passes a session ID, not a raw sandbox ID. This is a workspace-level concern (mapping sessions → sandboxes).

---

## Implementation Plan

### Phase 1: Define `Workspace` Interface + Rename `Session` methods
**Scope:** `pkg/env/session.go`, `pkg/env/workspace.go`
- Add `type Workspace interface` with `Create()`, `Get()`, `Exists()`, `Delete()`
- Remove public `Sandbox()`, `Status()`, `IsReady()`, `WaitReady()` from Session; add `SessionStatus` enum
- Move `Delete()` from Session to Workspace interface
- Keep `Exec()`, `ReadFile()`, `WriteFile()` as-is (these are tool-call paths, not lifecycle)

### Phase 2: Simplify Agent Loop Dependencies
**Scope:** `pkg/agent/loop.go`
- Remove `openShell`, `openShellClient`, `workspaceManager` fields
- Add single `workspace env.Workspace` field
- Change `GetSession()` to delegate to `l.workspace.Get()`
- Remove direct `l.openShell.CreateSandbox()`, `l.openShell.DeleteSandbox()` calls from `executeWithLLM()`
- Remove import of `openshell/v1` (replace with local types where needed)

### Phase 3: Enforce Workspace Creation Rules
**Scope:** `cmd/norman-server/sessions.go`, `workspaces.go`
- `CreateSession` handler calls `workspace.Create()` instead of `GetSession()` (which auto-creates)
- Add `EnsureWorkspace(workspaceID)` validation before task operations
- Tasks that reference a non-existent workspace return HTTP 404 with clear message

### Phase 4: Encapsulate Sandbox Concept
**Scope:** `pkg/env/context.go`, `workspace.go`
- Make `WorkspaceDefinition.SandboxName` unexported (`sandboxName`)
- Add getter: `func (w *WorkspaceDefinition) Name() string`
- Remove `SandboxName()` public method from WorkspaceManager; keep internal

### Phase 5: Clean Up Server Layer — Implementation Results
**Status:** ✅ Completed (PR #71, committed to `fix/empty-workspace-abort`)

**Scope:** `cmd/norman-server/sessions.go`, `pkg/env/workspace_interface.go`, `pkg/env/workspace.go`, `pkg/agent/loop_test.go`

| Change | Rationale |
|--------|-----------|
| Added `UpdateActivity(ctx, sessionId string)` to `Workspace` interface | Activity is per-session, not per-workspace-name. The handler has a `sessionID`, so the method looks up by session ID directly. Replaces `s.workspaceManager.UpdateLastActivity(workspaceId)`. |
| Added `Sessions() map[string]*Session` to `Workspace` interface | Already implemented on `WorkspaceManager` (line 290 of workspace.go); added to interface for completeness. |
| Replaced `s.workspaceManager.GetSession(ctx, wsId, sid)` → `l.Ws.Get(ctx, sid)` in `CreateSession()` | Semantic: `GetSession()` auto-creates (recoverOrCreate), `Get()` returns NotFound. In CreateSession we already created the session first, so verification via Get() is correct. |
| Replaced `s.workspaceManager.UpdateLastActivity(wsId)` → `l.Ws.UpdateActivity(ctx, sid)` | Session-ID keyed lookup instead of workspace-name keyed. |

**Design decisions preserved (not changed):**
- `s.workspaces` map in server struct kept — it's a server-level registry for workspace metadata (purpose, scope, tags), not replaced by the Workspace interface.
- `updateWorkspaceActivity()` and `checkWorkspaceIdle()` remain unchanged — these iterate `s.workspaces` to find workspace mappings for session IDs; they are server-level concerns.

**Post-Implementation Verification:**
- [x] `go build ./...` — compiles without errors
- [x] `go test ./...` — all tests pass (agent, env, agent/integration)
- [x] `make lint` — 0 issues
- [x] `git diff --stat` — 5 files changed, +55/-19 lines

---

**Deviation from original plan:** The original scope listed `bootstrap.go` and `tasks.go`, but the actual implementation focused on `sessions.go` (the core server handler). The `GetLogs()` routing through workspace was deferred to a future iteration.

### Phase 6: Update Tests
**Scope:** `pkg/agent/integration_test.go`, `loop_test.go`
- Replace `FakeOpenShellClient` with `FakeWorkspace` implementing `env.Workspace`
- Add test: `GetSession()` returns error if workspace not created
- Fix `TestRawSourcesContextInjection` nil-deref (add guard before `l.config.System.MaxTurns`)
- Fix `TestIntegrationExecuteWithLLMPersistsTurns` timing issue (use task completion callback instead of fixed 200ms sleep)

---

## Examples

### ✅ Good: Non-creating `Get()` pattern

```go
// Session creation in server handler — explicit, never auto-creates:
func (s *server) CreateSession(c echo.Context) error {
    session, err := s.workspace.Create(c.Request().Context(), req.SessionId)
    if err != nil {
        return c.JSON(400, map[string]string{"error": err.Error()})
    }
    return c.JSON(201, session)
}

// Agent loop submits task — fails cleanly if workspace missing:
func (l *Loop) GetSession(workspaceID string, sessionId string) (*env.Session, error) {
    return l.workspace.Get(context.Background(), sessionId)
    // Returns NotFound error if workspace doesn't exist.
    // No auto-creation. No sandbox creation inside the loop.
}
```

### ❌ Bad: Current auto-creation shortcut (to be removed)

```go
// BEFORE — auto-creates sandbox if one doesn't exist:
func (l *Loop) GetSession(workspaceID string, sessionId string) (*env.Session, error) {
    sb, err := client.GetSandbox(context.Background(), "default", sandboxName)
    if err == nil && sb != nil {
        return env.NewSession(client, "default", sb.Name, sessionId), nil
    }
    // Shortcut: creates new sandbox!
    ctxFile := l.buildContextFile(workspaceID)
    _, err = client.CreateSandbox(context.Background(), workspaceID, sandboxName, ctxFile, sessionId)
    // ...deploys roger...
    return session, nil  // ← auto-created, not user-requested
}
```

### ✅ Good: Single `Create()` path for sandbox creation

```go
// BEFORE — two paths to sandbox creation:
// Path A (server handler): workspaceManager.CreateWorkspace() → client.CreateSandbox()
// Path B (agent loop):    GetSession() → if nil → client.CreateSandbox()

// AFTER — single canonical path:
func (ws *workspaceManager) Create(ctx context.Context, sessionId string) (*Session, error) {
    // ONE path. No shortcuts. If sandbox exists, returns it.
    // If not, creates new + deploys skills + returns session.
}
```

### ✅ Good: Session status enum instead of raw `*v1.Sandbox`

```go
// BEFORE — leaks SDK type:
func (s *Session) Status(ctx context.Context) (*v1.Sandbox, error) { ... }

// AFTER — simple enum:
type SessionStatus string
const (
    SessionReady   SessionStatus = "ready"
    SessionRunning SessionStatus = "running"
    SessionStopped SessionStatus = "stopped"
    SessionError   SessionStatus = "error"
)

func (s *Session) Status() SessionStatus { ... }
```

---

## Trade-offs

| Trade-off | Chosen Approach | Rationale |
|-----------|----------------|-----------|
| `Get()` fails vs. auto-creates | **Fails with NotFound** | Enforces principle #3: tasks don't create workspaces. Explicit `Create()` is the only path. |
| Single `env.Workspace` interface vs. three separate dependencies | **One interface** | Simpler dependency graph; easier to mock in tests; no "which client do I use?" confusion. |
| Exposing `*v1.Sandbox` from Session vs. encapsulating | **Encapsulate** | Removes OpenShell knowledge from public API surface; agents only know sessions, not sandboxes. |
| `Delete()` on Workspace vs. Session | **On Workspace** | Lifecycle management (start/stop/delete) is a workspace concern, not a session concern. Sessions are ephemeral artifacts of workspaces. |
| Keeping `ReadFile()`/`WriteFile()` public on Session | **Keep** | These are tool-call paths (file I/O for the agent), not lifecycle operations. They belong on Session because that's where the agent invokes them. |

### Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| Breaking changes to existing `Session` callers (`Sandbox()`, `Status()`) | Add deprecation warnings; migrate all callers in Phase 4 (grep-and-update is straightforward) |
| `FakeWorkspace` mock for tests — must implement all interface methods | Start with minimal mock (Create, Get, Exists); add Delete/Session later |
| `GetLogs()` routing through workspace changes signature from `(sandboxID string)` to `(sessionId string)` | Add adapter method; server handlers update their calls |
| Auto-creation removal may cause first-task failures for users who relied on it | Add migration note in PR description; consider a "legacy auto-create" mode toggle in config during transition |

---

## Verification Criteria

The implementation is complete when:

1. ✅ `pkg/agent/loop.go` has exactly **one** `env.Workspace` field (no `openShell`, `openShellClient`, or `workspaceManager`)
2. ✅ `Session` has no public methods returning `*v1.Sandbox` or raw SDK types
3. ✅ `GetSession()` returns `NotFound` error if workspace doesn't exist (tested)
4. ✅ Only `Create()` path creates new sandboxes — verified by grep: no `client.CreateSandbox()` call outside `Workspace.Create()`
5. ✅ All existing tests pass (`go test ./...`)
6. ✅ No `openshell/v1` import in `pkg/agent/` (only in `pkg/env/`)

---

## Related Documents
- `./openshell-interaction.md` — OpenShell boundary model (confirms agent shouldn't see SDK directly)
- `./session-workflow-proposal.md` — Session lifecycle and WS event model
- `./workspace-lifecycle-proposal.md` — Workspace states (`active`, `stopped`, `purged`)
- `./workspace-definition-assessment.md` — Workspace identity definition
- `./workspace-skill-install-plan.md` — Skill installation in workspaces

---

*Draft · 2026-11-06 · Norman project · Awaiting approval to begin implementation*
