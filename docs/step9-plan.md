# Step 9 — Follow-up: real SDK, scheduler wiring, CLI archive, config tests

> **Status:** ✅ COMPLETE (merged to `main`)

---

## Problem Statement

After initial server scaffolding (Step 5), several follow-up items remained:
1. The OpenShell client was a stub (`FakeOpenShellClient`); needs real SDK integration with mTLS auth.
2. Scheduler results channel wasn't wired into the agent loop's notification path.
3. CLI had no `sessions archive` command (REST endpoint existed but no CLI wrapper).
4. Config defaults validation tests were missing for `NotifyConfig`.

---

## Current State (at time of planning)

- `pkg/env/fake_client.go` — `FakeOpenShellClient` implements `OpenShellClient` interface but returns dummy data.
- `pkg/scheduler/` — scheduler exists but `Start()` isn't called from `main()`; no results channel injected into Loop.
- `cmd/norman/main.go` — `sessions` command exists but doesn't have an `archive` subcommand.
- `pkg/config/config.go` — `NotifyConfig` struct defined but no unit tests for defaults/validation.

---

## Proposed Architecture

### 1. Real SDK Integration (`pkg/env/sdk_adapter.go`)

| File | Purpose |
|------|---------|
| `pkg/env/sdk_adapter.go` | Wraps `*env.Client` (from NVIDIA OpenShell Go SDK) and adapts method signatures to match `OpenShellClient` interface; falls back to `FakeOpenShellClient` when SDK unavailable. |

**Design:**
- `SDKAdapter` struct implements `OpenShellClient` interface.
- On initialization: attempts to load `ca.crt`, `tls.crt`, `tls.key` from config paths.
- If SDK unavailable (e.g., dev environment without OpenShell installed), falls back to `FakeOpenShellClient`.

---

### 2. Scheduler Wiring (`pkg/scheduler/`)

| File | Purpose |
|------|---------|
| `pkg/scheduler/scheduler.go` | `Start()` method integrated into `main()`; results channel injected into agent loop via `WithResultsChannel()`. |

**Design:**
- Scheduler runs in background goroutine.
- On each scheduled task completion → pushes result to `chan scheduler.TaskResult`.
- Agent loop receives results and broadcasts via WebSocket hub.

---

### 3. CLI Archive Command (`cmd/norman/main.go`)

| Change | Purpose |
|--------|---------|
| Add `sessions archive <task_id>` subcommand | Sends `POST /api/sessions/archive` to server; confirms archival completion. |

**CLI usage:**
```bash
norman sessions archive <task_id>
```

---

### 4. Config Validation Tests (`pkg/config/config_test.go`)

| Test | Purpose |
|------|---------|
| `TestNotifyConfigDefaults` | Verifies zero/negative rate limits are rejected; default values applied correctly. |
| `TestNotifyConfigYAMLUnmarshal` | Validates YAML parsing of notify config section. |

---

## Data Model

```go
// pkg/env/sdk_adapter.go
type SDKAdapter struct {
    Client *env.Client
}

func (a *SDKAdapter) CreateWorkspace(ctx context.Context, name string) (*protocol.Workspace, error) { ... }
func (a *SDKAdapter) ExecCommand(ctx context.Context, workspaceName string, cmd string) (*sdk.ExecOutput, error) { ... }

// pkg/scheduler/task.go
type TaskResult struct {
    TaskID      string
    Status      string // "success", "failed"
    Output      string
    Error       string
}
```

---

## Integration Points

| Component | Change |
|-----------|--------|
| `pkg/env/sdk_adapter.go` | New file — SDK wrapper + fallback to FakeOpenShellClient. |
| `cmd/norman-server/main.go` | Call `scheduler.Start()` in `main()`; inject results channel into Loop. |
| `cmd/norman/main.go` | Add `sessions archive <task_id>` subcommand. |
| `pkg/config/config_test.go` | Add 7 unit tests for `NotifyConfig` defaults and validation. |

---

## Acceptance Criteria

- [ ] `norman server` starts with real OpenShell SDK client (not fake stub).
- [ ] Scheduler fires scheduled tasks; results pushed to connected WebSocket clients.
- [ ] `norman sessions archive <task_id>` sends POST request and confirms archival.
- [ ] `go test ./pkg/config/...` passes all 7 NotifyConfig tests.
- [ ] SDK fallback works gracefully when OpenShell is not installed (dev mode).

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| SDK unavailable in dev environments | `FakeOpenShellClient` fallback; log warning at startup. |
| Scheduler goroutine leaks on shutdown | Use `context.WithCancel()`; call `scheduler.Stop()` in server shutdown handler. |
| CLI archive command blocks until completion | Non-blocking: send request, return immediately with task status polling hint. |

---

## Dependencies on Other Steps

- **Step 5b** (agent loop): Results channel injection into Loop is the integration point for scheduler output.
- **Step 8** (notifications): Scheduler results flow through the same notification path as task completions.
- **Step 10** (server rename): Config paths must be consistent across server rename refactor.

---

*This is a plan draft. Actual implementation follows approved sub-steps.*
