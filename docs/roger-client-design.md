# Roger Client — Design Document

> A general-purpose, workspace-aware CLI client for Norman's API, designed to be invoked from scripts and the agent loop inside OpenShell sandboxes.
> **Status:** Implementation complete.

---

## 1. Motivation

Scripts and scheduled tasks running inside a Norman sandbox need to communicate with the user without consuming LLM agent budget. The current path (`stdout → resultsCh → agent loop → notifier`) is indirect and requires the agent to parse raw output into structured notifications.

**Goal:** A lightweight, self-contained binary that lets any script push notifications (and other API actions) directly to Norman's WebSocket hub, with zero LLM involvement.

---

## 2. Naming: `roger`

- **"Roger that"** — the act of acknowledging and delivering a message
- **Roger I of Sicily** (1031–1101) — etymological tie to "Norman" (William Ironside was Duke of Normandy; Roger II became first King of Sicily)
- Short, memorable, verb-friendly: `roger notify`, `roger ping`, `roger status`

---

## 3. Scope

| Feature | In scope | Out of scope (v1) |
|---------|----------|-------------------|
| Push notifications | ✅ | Slack/webhook routing (handled by Norman server) |
| Health check / ping | ✅ | — |
| Query workspace state | ✅ | — |
| Tail recent events for a session | ✅ | — |
| Session management | ❌ (future) | — |
| Task creation | ❌ (future) | — |

---

## 4. Architecture

```
┌─────────────────────────────────────────────────────┐
│  OpenShell Sandbox                                    │
│                                                     │
│  ~/.config/norman/context.json  ← auto-injected     │
│    workspace_id, workspace_name                     │
│  roger                          ← CLI binary        │
│  (script) ──► roger notify ...                       │
│               ──► HTTP POST /api/notifications       │
└─────────────────────────────────────────────────────┘
                      │  (direct HTTP, no localhost!)
                      ▼
┌─────────────────────────────────────────────────────┐
│  Norman Server ({Host}:{ServerPort})                 │
│                                                     │
│  /api/notifications POST → WSHub.BroadcastEvent()   │
│  /api/workspaces/{id} GET → workspace metadata      │
│  /api/sessions/{id}/events GET → recent events      │
└─────────────────────────────────────────────────────┘
```

**Key data flow:**

1. `NewWithClient(cfg, ...)` receives the Norman config; `NORMAN_SERVER_URL` env var is set at sandbox creation
2. `Loop.buildContextFile()` extracts `Host` + `ServerPort` → injects `NORMAN_SERVER_URL` into sandbox env (not context.json)
3. `CreateSandbox(ctx, workspace, name, ctxFile)` injects context.json **without** `server_url` into sandbox
4. `roger` reads `$NORMAN_SERVER_URL` env var on startup → resolves target server from the context file
5. `roger` makes authenticated HTTP calls directly to Norman's API

**Key design decisions:**

1. **Auto-detection, no `--workspace` flag** — reads `~/.config/norman/context.json` injected at sandbox creation. Contains `workspace_id`, `workspace_name`.
2. **General-purpose CLI** — not just a notify tool; covers multiple API interactions via subcommands (`notify`, `ping`, `status`, `log`).
3. **Structured output** — JSON on stdout (machine-parseable), human-readable summary on stderr (user-facing).
4. **Auth encapsulated** — no mTLS paths in context file (removed; agents don't call OpenShell directly). No CLI flags for credentials.
5. **Outside the agent loop** — direct HTTP POST, zero LLM budget consumed per invocation.

---

## 5. Context File Format (`~/.config/norman/context.json`)

Injected at sandbox creation time by Norman's bootstrap:

```json
{
  "workspace_id": "a1b2c3d4-e5f6-...",
  "workspace_name": "api-migration"
}
```

> **Note:** `server_url` is no longer stored in the context file. The server address is provided via the `$NORMAN_SERVER_URL` environment variable at sandbox creation time. This avoids coupling the context file to a specific deployment URL and makes it portable across environments (dev, staging, prod).
>
> **mTLS removed:** Context file no longer carries cert/key paths. Agents don't call OpenShell directly (that's server-internal), and local cert paths wouldn't work in a remote sandbox without uploading them. Norman's API auth is handled separately.

The `roger` binary reads `$NORMAN_SERVER_URL` env var on startup to resolve the target server address. If the env var is absent, it falls back to `http://localhost:8080`.

---

## 6. CLI Interface (v1)

```bash
# Push a notification
roger notify --title "CPU > 90%" --body "Node X is overloaded" --severity warn

# Health check — pushes a lightweight event to the session's WS channel
roger ping

# Query workspace state
roger status [--json]

# Tail recent events for the current session
roger log --lines 20

# Help / version
roger --help
roger --version
```

**Output convention:**
- `stdout`: structured JSON (parseable by scripts)
- `stderr`: human-readable formatted text (visible to user)
- Exit code: `0` = success, `1` = API error, `2` = auth/config error

---

## 7. Session Identity Model

The session has a **single immutable identifier**: the Norman API session UUID (`sessionId`). This is set at construction and never mutated.

```go
type Session struct {
    client      *Client
    provider    string
    sandbox     string   // OpenShell sandbox name (internal)
    namespace   string
    sessionId   string   // Norman API session UUID (user-facing, immutable)
}
```

- `SessionID()` → returns the UUID (never falls back to sandbox name)
- `SandboxName()` → removed (dead code; sandbox name is internal only)
- `SetSessionId()` → removed (dead code; sessionId is set at construction)

The session cache (`m.sessions`) is indexed by **session ID**, not workspace name, allowing multiple sessions per workspace:

```go
sessions map[string]*Session // session ID -> session
```

**Lifecycle:**
1. `POST /api/workspaces` → creates OpenShell sandbox (no session)
2. `POST /api/sessions` → calls `GetSession(workspaceID, uuid)` → creates Session(uuid), caches it under the UUID key
3. `$SESSION_ID` inside sandbox = user-facing UUID

---

## 8. Changes Required in Norman (Implemented)

| Component | Change | Status |
|-----------|--------|--------|
| **Sandbox creation** (`pkg/env/client.go`) | Write `~/.config/norman/context.json` into new sandboxes at creation time | ✅ Done |
| **Context file injection** (`pkg/env/workspace.go`) | `CreateWorkspace` injects context.json without mTLS paths; server address provided via `$NORMAN_SERVER_URL` env var | ✅ Done |
| **Agent loop** (`pkg/agent/loop.go`) | `buildContextFile()` no longer extracts mTLS paths; `GetSession` and task sandbox creation pass proper ContextFile | ✅ Done |
| **mTLS removed** (`pkg/env/client.go`) | `GetMTLSPaths()` removed from context file (agents don't call OpenShell) | ✅ Done |
| **Interface update** (`pkg/agent/loop.go`) | `OpenShellClient` interface simplified (no mTLS paths in ContextFile) | ✅ Done |
| **API spec** (`api-spec.yaml`) | No changes needed — `/api/notifications`, `/api/workspaces/{name}`, `/api/sessions/{id}/events` already exist | ✅ N/A |
| **WSHub** (`pkg/protocol/types.go`) | Already supports `WebSocketEvent` with `SessionId` field for per-session targeting | ✅ N/A |
| **Notifier** (`pkg/notify/notifier.go`) | Already has `Notify()` method — `roger` calls HTTP API which routes through same notifier path | ✅ N/A |
| **Deployment** | `roger` installed as a builtin skill (`pkg/skills/builtin/roger/SKILL.md`) copied to `~/.local/bin/roger` at sandbox creation | ✅ Done |

---

## 9. Implementation Plan

| Phase | Deliverable | Files Changed |
|-------|-------------|---------------|
| P1 | Design doc (this file) | `docs/roger-client-design.md` (new) |
| P2 | `roger` CLI binary (Go) | `cmd/roger/` (new), `go.mod` update |
| P3 | Sandbox context injection | `pkg/env/client.go`, `pkg/env/workspace.go` (`CreateSandbox`) |
| P4 | Skill-based deployment | `pkg/skills/builtin/roger/SKILL.md` (new), `pkg/skills/activate.go` (extend) |
| P5 | Integration test for context file + roger call | `pkg/env/workspace_test.go` additions |

---

## 10. Risks & Trade-offs

| Risk | Mitigation |
|------|-----------|
| Context file not present at sandbox start | Aborts with "you can only run roger within a workspace" if no URL is available |
| Multiple sessions in one workspace | `roger ping` targets current session via `$SESSION_ID` env var (falls back to `"*"` if unset) |
| API auth changes (mTLS rotation) | Context file can be updated; binary re-reads on each invocation |
| Binary distribution overhead | ~2-3 MB Go static binary; acceptable for sandbox images |

---

## 11. Distribution Model: roger as a Skill (not custom image)

Instead of baking `roger` into an OpenShell template image, it is installed as a **binary skill** that Norman deploys into each sandbox at creation time. This keeps the binary in sync with the host installation and avoids a separate image rebuild step.

### 11.1 Skill entry (`pkg/skills/builtin/roger/SKILL.md`)

```yaml
---
name: roger
description: "General-purpose Norman API client for notifications, status checks, and log tailing."
activation: on-demand
---
```

The skill is registered as a **builtin** entry (bundled with the Norman binary) rather than cloned from a git repo.

### 11.2 Deployment target: `~/.local/bin/roger`

| Property | Value | Rationale |
|----------|-------|-----------|
| Target path | `~/.local/bin/roger` | XDG-standard, user-scoped (each sandbox gets its own copy) |
| Permissions | `chmod +x` by deploy script | Executable without root access |
| PATH injection | `export PATH="$HOME/.local/bin:$PATH"` appended to `~/.bashrc` | Discoverable as `roger` without full path |

The deploy script (`deploy.sh`) runs at sandbox creation:
```bash
cp /path/to/host/roger "$HOME/.local/bin/roger"
chmod +x "$HOME/.local/bin/roger"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.bashrc"
```

The sandbox already has write access to `~`, so no special sandbox config is needed.

### 11.3 Deployment flow

```
NewWithClient(cfg, ...)
    │
    ▼
Loop.GetSession(workspaceID) ──► CreateSandbox(ctx, workspace, name, ctxFile)
    │                               │
    │                               ├─ WriteContextFile() → ~/.config/norman/context.json
    │                               └─ DeployToSandbox("roger", session)
    │                                       │
    │                                       ▼
    │                               copy host roger → ~/.local/bin/roger
    │                                       chmod +x
    │                                       append PATH to ~/.bashrc
    │
    ▼
  Sandbox ready: context.json + roger executable available
```

### 11.4 Why not a custom image?

| | Custom Image | Skill-based (this design) |
|---|---|---|
| Update cadence | Rebuild image for every change | Copy new binary on next sandbox creation |
| Binary size | Baked into image layers | One copy per sandbox (~2–3 MB) |
| Version pinning | Image tag = version | Host binary path = version |
| Complexity | Dockerfile + CI pipeline | SKILL.md + deploy.sh (no build step) |

The skill approach means `roger` stays in the same update cycle as Norman itself — when you `go build -o roger`, the next sandbox creation picks up the new binary automatically. No separate image rebuild or registry push.

### 11.5 Integration points

| Component | Change |
|-----------|--------|
| `pkg/skills/builtin/roger/SKILL.md` | New skill metadata entry |
| `pkg/skills/activate.go` | Extend `DeployToSandbox` to handle binary skills (file copy instead of git clone) |
| `pkg/env/workspace.go` | Call `Activator.DeployToSandbox("roger", session)` during sandbox creation |

---

## 12. Implementation Plan (updated)

| Phase | Deliverable | Files Changed |
|-------|-------------|---------------|
| P1 | Design doc (this file) | `docs/roger-client-design.md` (new) |
| P2 | `roger` CLI binary (Go) | `cmd/roger/` (new), `go.mod` update |
| P3 | Sandbox context injection | `pkg/env/client.go`, `pkg/env/workspace.go` (`CreateSandbox`) |
| P4 | Skill-based deployment | `pkg/skills/builtin/roger/SKILL.md` (new), `pkg/skills/activate.go` (extend) |
| P5 | Integration test for context file + roger call | `pkg/env/workspace_test.go` additions |

---

*Draft — v1.0 · 2026-10-24*
