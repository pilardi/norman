---
title: "Session History — Gap Assessment"
type: assessment
status: draft
date: 2026-11-07
author: Pablo (via goose)
related: [session-workflow-proposal.md, task-management-assessment.md]
---

# Session History — Gap Assessment

> **Status:** Under Investigation  
> **Date:** 2026-11-07  
> **Author:** Pablo (via goose)

## TL;DR

- `GET /api/sessions/{id}/history` always returns `[]` — the endpoint reads `sessMap["turns"]` but nothing ever writes to that key.
- The CLI client (`cmd/norman/chat.go`) builds a local `turns []map[string]interface{}` during the REPL loop but never sends it to the server or persists it.
- There is **no** persistence of conversation turns at the session level — only task-level JSONL turns exist (`pkg/memory/store.go:PersistTurn`).
- The spec defines `SessionTurn` (role, content, timestamp) and a `/history` GET endpoint, but the implementation gap means history is never populated.

## Background

The session workflow proposal (2026-10-16) established sessions as the primary unit of interactive conversation. It covers:
- Session creation (`POST /api/sessions`)
- WebSocket subscription for real-time events
- Message sending (`POST /api/sessions/{id}/messages` → `SendMessage` handler)
- Session lifecycle (active → idle → archived)

What it **didn't** cover: how the conversation turns are persisted and retrieved. The spec includes a `SessionTurn` schema and `/history` endpoint, but implementation never wires them together.

## Problem

When a user runs `/history`, they get an empty array even though the session has clearly been active (messages sent, tasks run, responses received). The gap is:

| Layer | What Exists | Gap |
|-------|-------------|-----|
| **Spec** (`api-spec.yaml`) | `SessionTurn` schema + `GET /api/sessions/{id}/history` | ✅ Defined |
| **Server handler** (`sessions.go:298`) | `SessionHistory()` reads `sessMap["turns"]` | ✅ Reads from store |
| **Client REPL** (`chat.go:255-370`) | Builds local `turns []map[string]interface{}` | ❌ Never sent to server |
| **Store** (`store.go`) | `PersistTurn(taskID, turn)` for tasks; no session-level equivalent | ❌ No session turns persistence |

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `pkg/memory/store.go:PersistTurn(taskID, turn)` | Writes to `base/turns/{taskID}.jsonl` (JSONL format). **Task-level only** — no session counterpart. |
| `pkg/memory/store.go:LoadTurns(taskID)` | Reads JSONL turns file. Again task-scoped. |
| `cmd/norman-server/sessions.go:310-321` (`SessionHistory`) | Reads `sessMap["turns"]` from session store map. But nothing writes there. |
| `cmd/norman/chat.go:255-370` (`chatLoop`) | Builds local `turns []map[string]interface{}` per REPL session. Never sent to server. |
| `pkg/agent/loop.go:357` (`SendMessage`) | Returns `AgentResponse{SessionID, Response}` — a simple string. No turn metadata returned. |
| `api-spec.yaml:SessionTurn` schema | Defines `{role, content, timestamp}`. ✅ Matches what's built locally in chat.go. |

## Questions & Answers

**Q1:** Should turns be persisted per-session (as JSONL like tasks) or stored inside the session's JSON map?  
**A:** Per-session JSONL (`base/turns/{sessionID}.jsonl`) is preferable — it supports incremental append writes, survives crashes, and matches the existing `PersistTurn` pattern. The session store map approach (`sessMap["turns"]`) is fragile (in-memory only until save).

**Q2:** Should the assistant response content be captured as-is from `AgentResponse.Response`, or should it include tool calls / structured metadata?  
**A:** For MVP, capture the plain text response. Tool call details can be added later as a separate turn type (`tool_use`). The spec's `SessionTurn.role` enum already supports `[user, assistant, system]`.

**Q3:** Does `SendMessage` need to return turn data in its response, or should persistence happen on the server side after receiving the message?  
**A:** Server-side persistence is correct — the client sends a message, the loop processes it and returns a response; the server should then persist both turns (user input + assistant response) atomically.

## Observations / Design Notes

### Current Flow (Broken)

```
CLI REPL (chat.go:250-370)
  ── user types "hi" ──▶
  ├── builds local turn: {"role":"user","content":"hi",...}
  ├── POST /api/sessions/{id}/messages → SendMessage handler
  │   └── loop.SendMessage() returns AgentResponse{Response: "..."}
  ├── builds local turn: {"role":"assistant","content":"...",...}
  └── (turns discarded when REPL exits)

Server SessionHistory (sessions.go:298-321)
  ── reads sessMap["turns"] ──▶ [] (never written)
```

### Where Turns Should Be Persisted

The most natural hook is **inside `SendMessage` handler** (`cmd/norman-server/sessions.go:147`), right after `loop.SendMessage()` returns and before the JSON response is sent back. At that point we have:
- User message text (from request body)
- Assistant response text (from `AgentResponse.Response`)

Both should be appended as turns to a per-session JSONL file.

### Trace Through Real Code

```
cmd/norman-server/sessions.go:164 → resp, err := s.loop.SendMessage(ctx, id, req.Message, req.Provider)
pkg/agent/loop.go:393 → return &AgentResponse{SessionID: sessionID, Response: string(task.Output)}
cmd/norman-server/sessions.go:175 → return c.JSON(http.StatusOK, resp)  ← turns never persisted here
```

The `req.Message` is available at line 154 (`req.Message`). The response text is at `resp.Response` (line 168). Both are accessible for persistence.

## Trade-offs

| Approach | Pros | Cons |
|----------|------|------|
| **Per-session JSONL** (`base/turns/{sessionID}.jsonl`) | Matches existing `PersistTurn`; incremental append; survives crashes | Adds new file per session; cleanup on archive/delete needed |
| **Inside session store map** (`sessMap["turns"]`) | Single file per session; no extra files | In-memory until save; harder to append incrementally |
| **Append to task-level turns** | Reuses existing `PersistTurn` | Mixes session and task turns; unclear ownership |

## Verification Criteria

- `curl http://localhost:8080/api/sessions/{id}/history` returns populated turns after a conversation
- Each turn has `{role, content, timestamp}` matching the spec's `SessionTurn` schema
- Turns are in chronological order (most recent N returned by default)
- History survives session restart (persists to disk)

## Client Command Exposure

The `/history [N]` slash-command is **already wired** in the CLI client (`cmd/norman/chat.go:293-450`):

```go
case "history":
    showHistory(sessionID, cmdArgs)
```

It calls `client.SessionHistory(ctx, sessionID, params)` which hits `GET /api/sessions/{id}/history?limit=N`. The current code path is:

1. CLI resolves session name → UUID via `resolveSessionID()`
2. Calls `protocol.SessionHistory()` (generated client method)
3. Server's `SessionHistory()` handler reads `sessMap["turns"]`
4. Returns JSON array of turns

**The gap is purely server-side**: nothing writes to `sessMap["turns"]`, so the command works but always returns `[]`. Once persistence is added (see Design section below), this client command will work end-to-end without any CLI changes.

## Open Questions for Implementation

- **Q:** Should `/history` accept a session name alias (e.g., `/history test`) or only UUID?  
  **A:** Already handled — `resolveSessionID()` maps names to UUIDs before calling the server.

- **Q:** Should the client format turns as readable text, or just print raw JSON?  
  **A:** Current code at line 452 iterates and prints `{role}: {content}` per turn — human-readable output, not raw JSON. ✅

- **Q:** Does `/history` need to work offline (local REPL only) or is server connectivity required?  
  **A:** Requires server (`getProtocolClient()`). If server is down, it falls back gracefully with an error message.
