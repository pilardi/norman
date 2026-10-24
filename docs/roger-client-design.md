# Roger Client — Design Document

> A general-purpose, workspace-aware CLI client for Norman's API, designed to be invoked from scripts and the agent loop inside OpenShell sandboxes.
> **Status:** Draft — pending implementation review.

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
│    server_url = http://{Host}:{ServerPort}          │
│    mtls_cert_path, mtls_key_path                    │
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

1. `NewWithClient(cfg, ...)` receives the Norman config
2. `Loop.buildContextFile()` extracts `Host` + `ServerPort` → builds `server_url`
3. `CreateSandbox(ctx, workspace, name, ctxFile)` injects context.json into sandbox
4. `roger` reads context.json on startup → resolves server URL + mTLS paths
5. `roger` makes authenticated HTTP calls directly to Norman's API

**Key design decisions:**

1. **Auto-detection, no `--workspace` flag** — reads `~/.config/norman/context.json` injected at sandbox creation. Contains `workspace_id`, `server_url`, and optional auth config.
2. **General-purpose CLI** — not just a notify tool; covers multiple API interactions via subcommands (`notify`, `ping`, `status`, `log`).
3. **Structured output** — JSON on stdout (machine-parseable), human-readable summary on stderr (user-facing).
4. **Auth encapsulated** — uses mTLS certs or session token from context file; no CLI flags for credentials.
5. **Outside the agent loop** — direct HTTP POST, zero LLM budget consumed per invocation.

---

## 5. Context File Format (`~/.config/norman/context.json`)

Injected at sandbox creation time by Norman's bootstrap:

```json
{
  "workspace_id": "a1b2c3d4-e5f6-...",
  "workspace_name": "api-migration",
  "server_url": "http://127.0.0.1:8080",
  "mtls_cert_path": "/path/to/cert.pem",
  "mtls_key_path": "/path/to/key.pem"
}
```

**How `server_url` is populated:** The agent loop extracts `Host` and `ServerPort` from the Norman config (`cfg.System.Host`, `cfg.System.ServerPort`) and constructs `http://{Host}:{ServerPort}`. This ensures `roger` inside a sandbox can reach the actual Norman server (not `localhost`).

**How mTLS paths are populated:** The `env.Client` stores its TLS config at construction time. `GetMTLSPaths()` returns the cert/key file paths, which are injected into the context file. If no mTLS is configured, these fields are empty strings.

The `roger` binary reads this file on startup to resolve the target server and auth credentials. If absent, it falls back to reading `$NORMAN_SERVER_URL` env var or querying a well-known endpoint.

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

## 7. Changes Required in Norman (Implemented)

| Component | Change | Status |
|-----------|--------|--------|
| **Sandbox creation** (`pkg/env/client.go`) | Write `~/.config/norman/context.json` into new sandboxes at creation time | ✅ Done |
| **Context file injection** (`pkg/env/workspace.go`) | `CreateWorkspace` now accepts `serverURL` and populates all context fields (workspace_id, workspace_name, server_url, mtls_cert_path, mtls_key_path) | ✅ Done |
| **Agent loop** (`pkg/agent/loop.go`) | `buildContextFile()` extracts Host+ServerPort from config; `GetSession` and task sandbox creation pass proper ContextFile | ✅ Done |
| **mTLS support** (`pkg/env/client.go`) | Added `GetMTLSPaths()` method on `Client`; stored TLS config in Client struct | ✅ Done |
| **Interface update** (`pkg/agent/loop.go`) | `OpenShellClient` interface includes `GetMTLSPaths()` | ✅ Done |
| **API spec** (`api-spec.yaml`) | No changes needed — `/api/notifications`, `/api/workspaces/{name}`, `/api/sessions/{id}/events` already exist | ✅ N/A |
| **WSHub** (`pkg/protocol/types.go`) | Already supports `WebSocketEvent` with `SessionId` field for per-session targeting | ✅ N/A |
| **Notifier** (`pkg/notify/notifier.go`) | Already has `Notify()` method — `roger` calls HTTP API which routes through same notifier path | ✅ N/A |
| **Deployment** | `roger` binary must be distributed into sandboxes — baked into OpenShell template image or installable via a workspace skill | ⏳ Pending |

---

## 8. Implementation Plan

| Phase | Deliverable | Files Changed |
|-------|-------------|---------------|
| P1 | Design doc (this file) | `docs/roger-client-design.md` (new) |
| P2 | `roger` CLI binary (Go) | `cmd/roger/` (new), `go.mod` update |
| P3 | Sandbox context injection | `pkg/env/client.go` (`CreateSandbox`) |
| P4 | Integration test for context file + roger call | `pkg/env/workspace_test.go` additions |
| P5 | OpenShell template update to include `roger` binary | OpenShell config (external) |

---

## 9. Risks & Trade-offs

| Risk | Mitigation |
|------|-----------|
| Context file not present at sandbox start | Fallback to `$NORMAN_SERVER_URL` env var + well-known endpoint discovery |
| Multiple sessions share one sandbox | `roger ping` targets current session via `$SESSION_ID` env var (set by OpenShell) |
| API auth changes (mTLS rotation) | Context file can be updated; binary re-reads on each invocation |
| Binary distribution overhead | ~2-3 MB Go static binary; acceptable for sandbox images |

---

*Draft — v0.1 · 2026-10-23*
