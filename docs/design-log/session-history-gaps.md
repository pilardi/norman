---
title: "Session History — Gap Assessment"
type: assessment
status: implementation-complete
date: 2026-11-07
author: Pablo (via goose)
related: [session-workflow-proposal.md, task-management-assessment.md]
---

# Session History — Gap Assessment

> **Status:** Core Implementation Complete ✅  
> **Date:** 2026-11-07  
> **Author:** Pablo (via goose)  
> **Test Coverage:** 6 new unit tests in `pkg/memory/store_test.go` covering persistence, loading, empty sessions, multi-session isolation, ID fields, and timestamps. All 29 store tests pass.

## TL;DR

- `GET /api/sessions/{id}/history` now returns **populated turns** — `SendMessage` handler persists each user/assistant turn to `base/turns/{sessionID}.jsonl`.
- The CLI client (`cmd/norman/chat.go`) still builds local turns for the REPL transcript display, but the server-side JSONL is now the authoritative source.
- Session-level persistence **is** implemented alongside task-level JSONL turns (`pkg/memory/store.go:PersistTurn`).
- The spec's `SessionTurn` schema (role, content, timestamp) and `/history` GET endpoint are fully wired and operational.

## Background

The session workflow proposal (2026-10-16) established sessions as the primary unit of interactive conversation. It covers:
- Session creation (`POST /api/sessions`)
- WebSocket subscription for real-time events
- Message sending (`POST /api/sessions/{id}/messages` → `SendMessage` handler)
- Session lifecycle (active → idle → archived)

What it **didn't** cover: how the conversation turns are persisted and retrieved. The spec includes a `SessionTurn` schema and `/history` endpoint, but implementation never wires them together.

## Problem (Resolved ✅)

When a user runs `/history`, they now get populated turns — persistence is wired into `SendMessage`. The gap that existed is closed:

| Layer | What Exists | Status |
|-------|-------------|--------|
| **Spec** (`api-spec.yaml`) | `SessionTurn` schema + `GET /api/sessions/{id}/history` | ✅ Defined |
| **Server handler** (`sessions.go:370-382`) | `SessionHistory()` reads per-session JSONL file | ✅ Reads from disk |
| **Client REPL** (`chat.go:255-370`) | Builds local `turns []map[string]interface{}` for display | ✅ Used locally; server-side is authoritative |
| **Store** (`store.go`) | `PersistSessionTurn(sessionID, turn)` + `LoadSessionTurns(sessionID)` | ✅ Session-level JSONL persistence added |

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `pkg/memory/store.go:PersistTurn(taskID, turn)` | Writes to `base/turns/{taskID}.jsonl` (JSONL format). **Task-level only** — inspired the session-level pattern. |
| `pkg/memory/store.go:LoadTurns(taskID)` | Reads JSONL turns file. Again task-scoped. |
| `cmd/norman-server/sessions.go:370-382` (`SessionHistory`) | Now reads per-session JSONL via `LoadSessionTurns()` — no longer empty. |
| `cmd/norman/chat.go:255-370` (`chatLoop`) | Builds local turns for REPL display; server-side JSONL is the authoritative source. |
| `pkg/agent/loop.go:393` (`SendMessage`) | Returns `AgentResponse{SessionID, Response}` — now persisted as assistant turn via hook in `sessions.go:207-226`. |
| `api-spec.yaml:SessionTurn` schema | Defines `{role, content, timestamp}`. ✅ Matches what's built locally in chat.go. |

## Questions & Answers

**Q1:** Should turns be persisted per-session (as JSONL like tasks) or stored inside the session's JSON map?  
**A:** Per-session JSONL (`base/turns/{sessionID}.jsonl`) is preferable — it supports incremental append writes, survives crashes, and matches the existing `PersistTurn` pattern. The session store map approach (`sessMap["turns"]`) is fragile (in-memory only until save).

**Q2:** Should the assistant response content be captured as-is from `AgentResponse.Response`, or should it include tool calls / structured metadata?  
**A:** For MVP, capture the plain text response. Tool call details can be added later as a separate turn type (`tool_use`). The spec's `SessionTurn.role` enum already supports `[user, assistant, system]`.

**Q3:** Does `SendMessage` need to return turn data in its response, or should persistence happen on the server side after receiving the message?  
**A:** Server-side persistence is correct — the client sends a message, the loop processes it and returns a response; the server should then persist both turns (user input + assistant response) atomically.

## Observations / Design Notes

### Current Flow (Working ✅)

```
CLI REPL (chat.go:250-370)
  ── user types "hi" ──▶
  ├── POST /api/sessions/{id}/messages → SendMessage handler
  │   ├── persists user turn to base/turns/{sessionID}.jsonl ← new
  │   ├── loop.SendMessage() returns AgentResponse{Response: "..."}
  │   └── persists assistant turn to base/turns/{sessionID}.jsonl ← new
  └── (local turns discarded; server-side JSONL is the source of truth)

Server SessionHistory (sessions.go:370-382)
  ── reads per-session JSONL file ──▶ [user turn, assistant turn, ...] ✅ populated
```

### Where Turns Are Persisted

Turns are persisted **inside `SendMessage` handler** (`cmd/norman-server/sessions.go:207-226`), right after `loop.SendMessage()` returns and before the JSON response is sent back. At that point we have:
- User message text (from request body)
- Assistant response text (from `AgentResponse.Response`)

Both are appended as turns to a per-session JSONL file via `s.store.PersistSessionTurn()`.

### Trace Through Real Code

```
cmd/norman-server/sessions.go:208 → resp, err := s.loop.SendMessage(ctx, id, req.Message, req.Provider)
pkg/agent/loop.go:393 → return &AgentResponse{SessionID: sessionID, Response: string(task.Output)}
cmd/norman-server/sessions.go:214-225 → PersistSessionTurn("user", req.Message) + PersistSessionTurn("assistant", resp.Response) ← new
cmd/norman-server/sessions.go:230 → return c.JSON(http.StatusOK, resp)  ← turns already persisted
```

The `req.Message` is available at line 198 (`req.Message`). The response text is at `resp.Response` (line 208). Both are accessible for persistence via `s.store.PersistSessionTurn()`.

## Trade-offs

| Approach | Pros | Cons |
|----------|------|------|
| **Per-session JSONL** (`base/turns/{sessionID}.jsonl`) | Matches existing `PersistTurn`; incremental append; survives crashes | Adds new file per session; cleanup on archive/delete needed |
| **Inside session store map** (`sessMap["turns"]`) | Single file per session; no extra files | In-memory until save; harder to append incrementally |
| **Append to task-level turns** | Reuses existing `PersistTurn` | Mixes session and task turns; unclear ownership |

## Verification Criteria ✅ All Met

- `curl http://localhost:8080/api/sessions/{id}/history` returns populated turns after a conversation
- Each turn has `{role, content, timestamp}` matching the spec's `SessionTurn` schema
- Turns are in chronological order (most recent N returned by default)
- History survives session restart (persists to disk via JSONL)

## Client Command Exposure

The `/history [N]` slash-command is **already wired** in the CLI client (`cmd/norman/chat.go:295`):

```go
case "history":
    showHistory(sessionID, cmdArgs)
```

It calls `client.SessionHistory(ctx, sessionID, params)` which hits `GET /api/sessions/{id}/history?limit=N`. The code path is:

1. CLI resolves session name → UUID via `resolveSessionID()`
2. Calls `protocol.SessionHistory()` (generated client method)
3. Server's `SessionHistory()` handler reads persisted JSONL turns
4. Returns JSON array of turns

The top-level `norman sessions history` command also exposes the `--json` flag (`main.go:346`) for programmatic/raw output. The slash-command `/history [N] [--json]` in chat.go delegates to `showHistory()` which parses args and passes them through.

**With server-side persistence now implemented**, the gap is closed — `/history` returns actual conversation turns instead of `[]`.

## Integration Verification ✅ Done

Verified end-to-end:
1. Start `norman-server` with a workspace active
2. Send messages via CLI (`./dist/norman open`) or direct API calls
3. Confirm turns appear in chronological order with `{role, content, timestamp}` fields
4. Verified turns persist across server restarts (JSONL survives)

## Archive Cleanup Status ⬜ Deferred

`DeleteSession()` removes the session JSON file but does NOT remove its `.jsonl` turns file. `handleSessionArchive()` similarly does not clean up turns files. This is a low-priority future concern — orphaned `.jsonl` files accumulate on archive/delete until explicitly cleaned.

## Implementation Status

| Component | Status | Notes |
|-----------|--------|-------|
| `PersistSessionTurn(sessionID, turn)` | ✅ Done | Added to `pkg/memory/store.go:742` — appends JSONL per session |
| `LoadSessionTurns(sessionID)` | ✅ Done | Added to `pkg/memory/store.go:785` — reads JSONL file with legacy 8-char prefix fallback |
| `SendMessage` persistence hook | ✅ Done | Added to `cmd/norman-server/sessions.go:207-226` — persists user + assistant turns atomically |
| `/history --json` CLI flag | ✅ Done | Defined in `main.go:346`, parsed in `showHistory()` at `sessions.go:137` |
| `/history` slash-command | ✅ Done | Wired in chat.go:295, delegates to `showHistory(sessionID, cmdArgs)` |
| Server-side persistence integration test | ✅ Done | Verified end-to-end with running server + CLI |
| Archive cleanup (turns files) | ⬜ Deferred | `DeleteSession()` and `handleSessionArchive()` do NOT remove `.jsonl` turns files |

## Open Questions for Implementation

- **Q:** Should `/history` accept a session name alias (e.g., `/history test`) or only UUID?  
  **A:** ✅ Already works — `resolveSessionID()` matches by full name or first-8-chars-of-UUID prefix. No changes needed here.

- **Q:** Should the client format turns as readable text, or just print raw JSON?  
  **A:** Human-readable by default (current behavior). `--json` CLI flag defined in `main.go:346`, parsed in `showHistory()` at `sessions.go:137`. ✅ Done.

- **Q:** Does `/history` need to work offline (local REPL only) or is server connectivity required?  
  **A:** Client is thin — no offline support needed. Requires server. ✅

- **Q:** Should archive/delete sessions clean up their turns files?  
  **A:** Not yet implemented. `DeleteSession()` in `sessions.go` removes the session JSON but not its `.jsonl` turns file. This is a future cleanup concern (low priority for MVP). ⬜ Deferred.

## Implementation Notes

- The top-level `norman sessions history` command parses `--json` from `cmd.Args()` in `main.go:357-364`, builds `extraArgs`, and passes them to `showHistory()`.
- The `/history [N]` slash-command in chat.go delegates directly to `showHistory(sessionID, cmdArgs)` — the same function handles both CLI flag and slash-command arg parsing.
- When `--json` is present, prints formatted JSON (`[]map[string]interface{}`) instead of `{role}: {content}` per line.
- No changes needed to server-side `SessionHistory` handler or protocol types — just client formatting.
