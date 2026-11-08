---
title: "Codex Structured Output Pattern for Norman Tasks"
type: proposal
status: draft
date: 2026-11-08
author: pablo (via tool)
related: [task-management-assessment.md, session-workflow-proposal.md]
---

# Codex Structured Output Pattern for Norman Tasks

## TL;DR

Codex CLI's `--output-schema` + `--json` flags provide a proven two-tier output model:
- **Structured JSON** (validated against a schema) — for machine consumption, downstream APIs, type-safe pipelines.
- **JSONL event stream** (`--json`) — for real-time monitoring, logging, and custom UIs built on the agent loop.

Norman's current `Task.output_path` is a single opaque file with no type discrimination. The proposal adds:
1. A `rich_output` field to `TaskCompletion` (and optionally `Task`) carrying structured metadata: `content_type`, `mime_type`, `render_hint`, and an optional `artifact_url`.
2. Enhance existing `/api/tasks/{id}/log` with a `?type=` query parameter (`raw` | `events`) — no new endpoint surface area, just one extra code path in the handler.
3. A `render_hint` enum (`text`, `markdown`, `html`, `json`, `pdf`, `chart`, `diagram`, `file_ref`) guiding client-side rendering without requiring the server to know every possible output format.

No single industry standard protocol exists yet. Codex is the closest working reference for an agent-native structured output model.

## Background

Norman currently stores task output as a single `Output []byte` (in `pkg/agent/loop.go`) and exposes it via a `/api/tasks/{id}/log` endpoint that returns plain text via `tail`. The `output_path` field in `Task` / `TaskCompletion` is a stub pointing to `/tmp/task-{id}.output`. Clients consuming the WebSocket or REST API see only raw text — there is no way to signal "this output is a chart," "this is a downloadable PDF," "this is structured JSON matching schema X," or "this references a workspace file at `@/path/to/file.md`."

As tasks produce increasingly diverse artifacts (HTML pages, Mermaid diagrams, TypeScript definitions, JSON data, PDFs), the plain-text model becomes a bottleneck for smart clients that could render differently based on content type.

## Problem

The specific gap: Norman's task output is a single opaque `[]byte` with no type metadata, no rendering hints, and no structured schema validation. Clients cannot decide how to present results (rich text vs. HTML artifact vs. downloadable file vs. JSON data) because the protocol carries zero discriminators beyond "here's some bytes."

This limits:
- **Rich UI clients** from rendering charts, diagrams, or Markdown inline.
- **Automation pipelines** from consuming structured outputs without brittle text parsing.
- **Audit trails** from distinguishing "this was a file edit" from "this was a shell command result."

## Prior Art / Adjacent Mechanisms: Codex CLI Structured Output

Codex CLI (v0.2+) provides the closest working reference for an agent-native structured output model. It exposes three distinct output modes, each suited to a different automation tier:

### 1. Plain stdout (default)
The agent's final message lands on stdout as free-form text. Adequate for simple piping (`codex exec "summarise this diff" | tee summary.md`) but downstream consumers must parse text manually.

### 2. `--output-schema` JSON Schema validation
Pass a JSON Schema file and Codex validates the agent's final response against it. The output is guaranteed to conform to the schema's shape, making it safe for `jq` pipelines, database inserts, or downstream API calls.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "failures": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "test_name": { "type": "string" },
          "root_cause": { "type": "string" },
          "confidence": { "enum": ["high", "medium", "low"] }
        },
        "required": ["test_name", "root_cause"]
      }
    },
    "summary": { "type": "string" }
  },
  "required": ["failures", "summary"],
  "additionalProperties": false
}
```

Key flags: `--output-schema PATH` + `-o /path/to/output.json`. If validation fails, Codex retries internally before surfacing an error.

### 3. `--json` JSONL event stream
Switches stdout to a newline-delimited JSONL stream covering the full execution lifecycle. Every event is a self-contained JSON object with a `type` field:

| Event Type | Emitted When | Key Fields |
|---|---|---|
| `thread.started` | Session begins | `thread_id` (UUID) |
| `turn.started` | A new agent turn begins | — |
| `item.started` | Tool call or message begins | `item.id`, `item.type`, `item.status` |
| `item.updated` | Incremental progress on an item | `item.id`, updated fields |
| `item.completed` | Tool call or message finishes | `item.id`, `item.type`, `text`/result |
| `turn.completed` | Agent turn finishes | `usage` object (`input_tokens`, `cached_input_tokens`, `output_tokens`) |
| `turn.failed` | Turn encountered an error | Error details |
| `error` | Fatal session error | Error message |

**Item types** within `item.*` events: `agent_message`, `command_execution`, `reasoning`, `file_change`, `mcp_tool_call`, `web_search`, `plan_update`.

⚠️ **Schema drift warning**: In earlier versions (pre-v0.44.0), the item type field was named `item_type` and agent messages used `assistant_message` rather than `agent_message`. There is currently no schema version indicator in the event stream.

### Practical Patterns from Codex
- Filter events: `jq -c 'select(.type == "item.command_execution")'`
- Extract token usage: `jq -c 'select(.type == "turn.completed") | .usage'`
- Build real-time progress monitors via line-by-line consumer.
- Multi-step session chaining via `codex exec resume --last`.

### What Norman Can Borrow
1. **Structured output schema** — not just "here's text" but "here's JSON matching this schema." This is the most immediately useful addition for Norman's task completion model.
2. **Item-type discrimination** — distinguishing file edits from shell commands from messages enables richer client-side rendering (e.g., show a diff view for `file_change`, a terminal panel for `command_execution`).
3. **Token usage metadata** in completion events — useful for billing, budget tracking, and performance analytics.

## Questions & Answers

| Q | A |
|---|---|
| Q1: Should we add a full JSONL event stream endpoint (`/api/tasks/{id}/events`) or just enrich `TaskCompletion` with structured fields? | Start with enriched `TaskCompletion` (field 3 below). The event stream is valuable but adds significant complexity to the server loop. Can be phased in later as `/api/tasks/{id}/events`. |
| Q2: Should `render_hint` be a closed enum or open-ended? | Closed enum for known types (`text`, `markdown`, `html`, `json`, `pdf`, `chart`, `diagram`, `file_ref`). Add an "other" catch-all. Open-ended strings add parsing ambiguity; closed enums allow clients to switch on known values and fall back gracefully. |
| Q3: How do we handle multi-page outputs (e.g., a PDF report)? | Store the file at `output_path` and set `content_type: "pdf"` + `mime_type: "application/pdf"`. The client can serve it via a download endpoint or embed if HTML. No need to inline binary data in the API response. |
| Q4: Does Codex's `--output-schema` pattern require Norman to accept schema definitions from clients? | Not necessarily. Norman can define a set of built-in schemas (e.g., "chart output," "JSON report") and reference them by name. Alternatively, accept an optional `schema_url` field pointing to a JSON Schema file in the workspace. Simpler: just carry `content_type` + `mime_type` without requiring schema validation at the server level. |
| Q5: How do we handle backward compatibility with existing clients? | Add new fields as `nullable` / optional. Existing clients that don't know about `rich_output` simply ignore it (JSON `additionalProperties: true` in OpenAPI handles this). The `output_path` field remains the legacy path for plain-text outputs. |

## Design

### Proposed Schema Additions to `TaskCompletion`

Add a `rich_output` object to both `Task` and `TaskCompletion`:

```yaml
RichOutput:
  type: object
  properties:
    content_type:
      type: string
      description: High-level category of the output (e.g., "text", "markdown", "html", "json", "pdf", "chart", "diagram", "file_ref")
    mime_type:
      type: string
      description: IANA media type (e.g., "application/pdf", "text/markdown")
    render_hint:
      type: string
      enum: [text, markdown, html, json, pdf, chart, diagram, file_ref]
      description: Hint for client-side rendering strategy
    artifact_url:
      type: string
      nullable: true
      description: URL or path to a rich artifact (HTML page, PDF download, etc.)
    structured_data:
      type: object
      additionalProperties: true
      nullable: true
      description: Machine-readable structured content (JSON object)
```

### Mapping Codex Patterns to Norman Fields

| Codex Concept | Norman Field | Purpose |
|---|---|---|
| `item.type` discriminator | `render_hint` enum | Tells client "how to render this" |
| `--output-schema` validated JSON | `structured_data` + `content_type` | Machine-consumable typed output |
| `usage` object | (existing, but could be elevated) | Token billing & analytics |
| `item.completed.text` | `output_path` (legacy) → `structured_data` (new) | Replace opaque `[]byte` with typed content |

### Rendering Pipeline (Client-Side)

```
TaskCompletion.rich_output.render_hint
  ├── "text"     → render as plain text in a code block
  ├── "markdown" → render as rendered Markdown (with GFM tables, links)
  ├── "html"     → embed HTML iframe or render inline
  ├── "json"     → render as formatted JSON tree
  ├── "pdf"      → trigger download or open in PDF viewer
  ├── "chart"    → render as interactive chart (D3, Chart.js)
  ├── "diagram"  → render as Mermaid/PlantUML diagram
  └── "file_ref" → resolve `artifact_url` to workspace file path
```

### API Enhancement (replaces standalone events endpoint)

Instead of adding a new `/api/tasks/{id}/events` REST endpoint, enhance the existing `/api/tasks/{id}/log` with a `?type=` query parameter:

```yaml
/api/tasks/{id}/log:
  get:
    parameters:
      - name: lines
        in: query
        schema:
          type: integer
          default: 50
      - name: type
        in: query
        description: Output format selector — "raw" (default, plain text tail) or "events" (structured JSONL event stream mirroring Codex's --json lifecycle)
        schema:
          type: string
          enum: [raw, events]
    responses:
      "200":
        description: Task log output in requested format
        content:
          text/plain:
            schema:
              type: string
          application/json:
            schema:
              type: string  # newline-delimited JSONL
```

**Why this is better than a new endpoint:**
- No new surface area to maintain; one endpoint, two modes.
- Clients that already call `/log` continue working unchanged (default `raw`).
- Smart clients can opt into structured events via `?type=events`.
- No WebSocket subscription needed for polling-based access.

**Trade-off:** The response content-type switches based on `type` — clients must check the header or query param to know how to parse the body. This is a minor UX cost compared to maintaining a second endpoint with its own routing, auth, and test coverage.

### WebSocket Enhancement (companion to REST)

The existing `/ws?session_id=...` carries coarse notifications (`task_progress`, `task_complete`, `notification`). Enhance it with a `?scope=` parameter:

```yaml
/ws:
  get:
    parameters:
      - name: session_id
        in: query
        required: true
      - name: scope
        in: query
        description: Event granularity — "session" (default, coarse notifications) or "events" (granular task-level events)
        schema:
          type: string
          enum: [session, events]
```

| Scope | Events emitted |
|-------|---------------|
| `session` (default) | `task_progress`, `task_complete`, `notification`, `context_budget_warning` |
| `events` | Granular lifecycle: `item.started`, `item.updated`, `item.completed`, `turn.completed`, `turn.failed` |

**Why this matters:** Smart clients that build real-time dashboards (charts, terminal panels, diff viewers) need granular per-item events — not just "the task progressed." The coarse `task_progress` notification doesn't tell you *what changed* (which file, which tool call).

**Trade-off:** Existing WebSocket subscribers get the current behavior unchanged (default `session`). New clients opt into granular events. No breaking change for existing consumers.

**Phase 1 — Schema Enrichment (no server loop changes)**
- Add `RichOutput` component to `api-spec.yaml`.
- Add `rich_output` field to `TaskCompletion` and `Task` (nullable).
- Regenerate Go types (`make generate`).
- Update `/api/tasks/{id}/log` response to include `rich_output` alongside existing plain-text output.

**Phase 2 — Server Loop Integration**
- In `pkg/agent/loop.go`, instead of accumulating raw text into `task.Output []byte`, track structured metadata: content type, render hint, and optional inline JSON data.
- On task completion, populate `TaskCompletion.rich_output` from the accumulated metadata.

**Phase 3 — Enhance both REST and WebSocket with structured events**
- Add `?type=events` to `/api/tasks/{id}/log` (pull-based access).
- Add `?scope=events` to `/ws?session_id=...` (push-based real-time access).
- Both expose the same granular event stream (`item.started`, `item.completed`, `turn.completed`) — just different transports.
- Existing subscribers keep default behavior (`raw` / `session`). No breaking change.

### Examples

✅ Good: A task that generates a bar chart of project metrics:
```json
{
  "rich_output": {
    "content_type": "chart",
    "mime_type": "image/png",
    "render_hint": "chart",
    "artifact_url": "/workspace/reports/q4-metrics.png"
  }
}
```

✅ Good: A task that produces structured JSON data for a dashboard:
```json
{
  "rich_output": {
    "content_type": "json",
    "mime_type": "application/json",
    "render_hint": "json",
    "structured_data": {
      "total_tasks": 42,
      "completed": 38,
      "failed": 4,
      "avg_duration_ms": 12500
    }
  }
}
```

❌ Bad: Setting `render_hint` to an arbitrary string like `"my-custom-format"` without a corresponding client renderer.

❌ Bad: Storing large binary content (e.g., PDF bytes) directly in `structured_data` — use `artifact_url` and serve the file separately.

## Trade-offs

| Trade-off | Detail |
|---|---|
| **Backward compatibility** | New fields are nullable/optional. Existing clients that don't know about `rich_output` simply ignore them (OpenAPI `additionalProperties: true`). Safe to deploy incrementally. |
| **Token cost** | Adding metadata fields to every task completion is negligible (~200-500 bytes per task). No significant token overhead. |
| **Client complexity** | Clients must implement renderer dispatch logic based on `render_hint`. However, this is the *purpose* of a "smart client" — it's not a bug but a feature requirement. |
| **Schema drift risk** | Mirroring Codex's warning: if Norman's `RichOutput` schema evolves (e.g., new enum values), clients need version-aware parsing. Mitigation: use closed enums and document deprecation policy. |
| **Server loop changes** | Phase 2 requires modifying `pkg/agent/loop.go` to track structured metadata instead of just accumulating text. This is a non-trivial refactor but well-scoped. |
| **No single standard** | Unlike HTTP's MIME types or JSON Schema, there is no universal "rich output" protocol yet. Norman would be pioneering this pattern in the agent ecosystem. |

## Implementation Results

*(To be appended after code changes are made and tested)*
