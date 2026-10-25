# OpenShell Interaction Model for Norman



> How Norman, its agent loop, tool calls, the `roger` client, and the server interact through the OpenShell sandbox boundary.

---

## 1. The Invisible Boundary

OpenShell is **not visible** to the agent or the tools it invokes. From the agent's perspective:

- It sees a normal filesystem, process table, and network stack.
- No special APIs, no `openshell` binary, no daemon processes are directly callable.
- Isolation is enforced at the kernel level (seccomp, Landlock, cgroups) — the agent cannot detect whether it is in a sandbox or on bare metal.

OpenShell's purpose: **contain agent actions** into a controlled environment without risking the host machine or exposing secrets. The agent never sees real credentials; OpenShell injects them only for approved endpoints.

---

## 2. Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│  Host / Cloud Infrastructure                                  │
│                                                               │
│  ┌──────────────────┐    TLS/JWT     ┌──────────────────┐   │
│  │  Gateway          │◄──────────────►│  Supervisor       │   │
│  │  (Control Plane)  │                │  (Trusted side)   │   │
│  └──────────────────┘                └────────┬─────────┘   │
│                                                │             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              OpenShell Sandbox Boundary              │    │
│  │                                                     │    │
│  │  ┌─────────────────────────────────────────────┐    │    │
│  │  │  Workload (Agent + Tools)                    │    │    │
│  │  │  • Agent loop                                │    │    │
│  │  │  • `roger` CLI                              │    │    │
│  │  │  • LLM provider binaries                     │    │    │
│  │  │  • Skill scripts                             │    │    │
│  │  └─────────────────────────────────────────────┘    │    │
│  │                                                     │    │
│  │  Sandbox (process owner) — untrusted, forwards      │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                               │
│  [Outer Network Fence: denies all egress except supervisor]   │
└─────────────────────────────────────────────────────────────┘
```

**Three connections tie a sandbox together:**

| Connection | Protection | Purpose |
|---|---|---|
| Supervisor ↔ Gateway | Gateway JWT over TLS | Policy, credentials, logs |
| Supervisor ↔ Sandbox | Mutual TLS + Sandbox JWT | Control plane for exec, terminal, process status |
| Agent → Supervisor (indirect) | Covered by Supervisor↔Sandbox channel | Tool calls, network requests, file access |

---

## 3. The Client (Norman) — Outside the Boundary

Norman is the **server-side orchestrator** that manages the sandbox lifecycle:

| Responsibility | Mechanism |
|---|---|
| Sandbox creation | `POST /api/workspaces` → compute driver provisions workload + supervisor |
| Context injection | Writes `~/.config/norman/context.json` (workspace_id, workspace_name) into new sandboxes |
| Session lifecycle | `POST /api/sessions` → creates session, caches under UUID key |
| Policy enforcement | Gateway delivers policy; formal verification flags risky changes before auto-approval |
| Credential management | Providers map service names to stored secrets; supervisor hands them out only where policy allows |

Norman itself **never runs inside** a sandbox. It is the operator that stands outside and manages sandboxes as resources.

---

## 4. The Agent Loop — Inside the Boundary

The agent loop (`pkg/agent/loop.go`) is the core runtime:

```
Agent Loop (inside sandbox)
    │
    ├── Reads workspace context from ~/.config/norman/context.json
    ├── Sends messages to Norman server via WebSocket hub
    ├── Receives tool calls, skill activations, scheduled tasks
    └── Invokes tools (LLM providers, MCP clients, filesystem, shell)
```

**Key constraints the agent observes:**

- **Fail-closed**: Launch is held until supervisor confirms controls; if the Supervisor↔Sandbox connection drops, the agent freezes.
- **No direct egress**: All network from the workload must pass through the mediated channel to the supervisor. The outer fence denies everything else (DNS, TCP, other hosts).
- **Process identity**: The sandbox identifies the calling executable from trusted `/proc` data — not a path the agent claims.
- **Single non-root identity**: Workload uses one user with no Linux capabilities.
- **Backpressure isolation**: Each TCP connection gets its own stream; a slow download cannot block DNS or exec.

---

## 5. Tool Calls Through the Boundary

When the agent invokes a tool (e.g., `llm_chat`, `filesystem_read`, `shell_exec`):

1. The sandbox intercepts the system call (via seccomp user notification for network, Landlock for filesystem).
2. The request is forwarded over the **Sandbox Protocol** (authenticated Unix socket / TCP / vsock depending on runtime) to the supervisor.
3. The supervisor checks it against policy — if allowed, it opens the real connection and relays traffic back.
4. If denied or awaiting review, the request blocks until a decision is made.

**The agent never sees credentials.** OpenShell adds them only to requests bound for approved endpoints (e.g., an LLM API key injected for a specific provider call).

---

## 6. The `roger` Client — Agent-Facing Bridge

`roger` is the **only direct HTTP client** that runs inside sandboxes, bridging scripts and tools back to Norman's server:

```
Script / Skill (inside sandbox)
    │
    ├── Reads $NORMAN_SERVER_URL from env + context.json
    ├── Makes authenticated HTTP POST to /api/notifications
    ├── Or queries /api/workspaces/{id}, /api/sessions/{id}/events
    └── Returns JSON on stdout, human-readable on stderr
```

**Design choices:**
- **No mTLS paths in context file** — agents don't call OpenShell directly (that's server-internal). Norman's API auth is handled separately.
- **Zero LLM budget consumed** — direct HTTP POST, bypassing the agent loop.
- **Session-scoped**: `$SESSION_ID` env var targets the correct session via `WebSocketEvent.SessionId`.
- **Skill-based deployment**: `roger` is installed as a builtin skill (`pkg/skills/builtin/roger/SKILL.md`) — copied to `~/.local/bin/roger` at sandbox creation. No custom image rebuild needed.

---

## 7. Network Constraints Summary

| Constraint | Effect on Agent |
|---|---|
| Outer network fence | Denies all egress except the protected Supervisor↔Sandbox channel |
| Mediated channel is the *only* egress path | Agent cannot reach a service, gateway, DNS, or private address directly |
| Policy prover (gateway) | Flags new credentialed reach, new HTTP methods, cloud metadata access — blocks auto-approval |
| Fail-closed guarantee | Agent cannot run before controls confirmed; freezes if connection drops |
| Token binding | Both Gateway JWT and Sandbox JWT are bound to one sandbox + one generation |

---

## 8. What the Agent Can See vs. Cannot See

| Visible | Hidden (kept by OpenShell) |
|---|---|
| `~/.config/norman/context.json` | Gateway signing key |
| `$NORMAN_SERVER_URL` env var | Provider credentials (injected at call time only) |
| `/proc` process data (sandbox-verified) | Private keys, client certificates |
| Filesystem via Landlock (policy-gated) | OpenShell daemon internals |

If the supervisor disconnects, the sandbox freezes the agent. Only the **same** supervisor process can reconnect and resume it — a new supervisor cannot take over a running sandbox, even with valid credentials.

---

## 9. Runtime Selection (How the Boundary is Built)

| Runtime | Supervisor Location | Transport | Egress Blocked By |
|---|---|---|---|
| Docker | Its own container | Authenticated Unix socket | Workload networking turned off |
| Podman | Its own container | Authenticated Unix socket | Workload networking turned off |
| Kubernetes | Its own pod | Private service + mTLS | NetworkPolicy allows only supervisor |
| VMA process on host | vsock | Authenticated vsock | Guest has no network device |

The runtime's job is to build the boundary and prove it's in place. Policy behaves the same way regardless of runtime.

---

## 10. Reference

- OpenShell Architecture: https://docs.nvidia.com/openshell/about/architecture
- OpenShell v0.1.5 docs: https://docs.nvidia.com/openshell/v0.1.5/about/overview
- Norman Roger Client Design: `docs/roger-client-design.md`

---

*Draft · 2026-10-24 · Norman project*
