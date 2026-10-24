---
name: "roger"
description: >
  General-purpose Norman API client for notifications, status checks, and log tailing.
  Provides a lightweight CLI interface to push notifications, query workspace state,
  and inspect recent session events — all without consuming LLM agent budget.
activation: "on-demand"
binary_path: "roger"
---

# Roger — Norman Workspace Client

A compiled binary skill that provides direct API access for scripts and scheduled tasks. Installed automatically at sandbox creation time.

## Usage Notes

- `roger notify` pushes a structured notification to the connected user via WebSocket.
- `roger ping` sends a lightweight health-check event to the current session's channel.
- `roger status` returns workspace metadata as JSON (stdout).
- `roger log --lines N` tails recent events for the active session.

## Deployment

Copied from the host installation into `~/.local/bin/roger` at sandbox creation.
No git clone or build step required — it is a pre-built artifact.

## Environment variables

The agent injects `SESSION_ID=<uuid>` into every command it runs. Commands that target
a specific session use this env var; if not set, they fall back to `"*"` (broadcast).

## Example Workflow

```bash
# Push a notification
roger notify --title "Build complete" --body "PR #42 merged successfully" --severity info

# Health check
roger ping

# Query workspace state (JSON on stdout)
roger status | jq '.name'

# Tail recent events
roger log --lines 10
```

## Context File

Reads `~/.config/norman/context.json` for workspace ID, workspace name, and mTLS credentials.
The server URL must be available (via `$NORMAN_SERVER_URL` env var) — roger aborts if not found.
