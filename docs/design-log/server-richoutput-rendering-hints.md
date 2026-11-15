---
title: "Server-Generated RichOutput Rendering Hints for TUI Client"
type: proposal
status: draft
date: 2026-11-15
author: pablo (via tool)
related: [bubbletea-tui-client-assessment.md, codex-structured-output-pattern.md, clean-client-plan.md]
---

# Server-Generated RichOutput Rendering Hints for TUI Client

## TL;DR

| Decision | Rationale |
|----------|-----------|
| **Server generates `RichOutput` metadata with `render_hint` and `structured_data`** | The server already has `detectRichOutput()` and `detectContentType()` logic in `pkg/agent/loop.go`. It can analyze the final task output and populate `RichOutput` with content type, render hint, and structured data. |
| **Include `rich_output` field in `WebSocketEvent` schema** | The TUI client needs to receive the `render_hint` and `structured_data` via WebSocket events (specifically `turn_completed` and `task_complete`) to render rich content properly instead of plain text. |
| **Current state: Server-side detection exists but is not sent via WebSocket** | The server's `detectRichOutput()` is called at task completion and populates `task.RichOutput`, but the `WebSocketEvent` schema does not include a `rich_output` field, and the event emission code does not include it. |
| **Client-side detection was implemented but has limitations** | The TUI client has `detectContentType()`, `isJSON()`, `hasHTMLTags()`, `hasMarkdown()` functions in `cmd/norman/tui/content_detector.go`, but streaming detection trims newlines and fails to properly detect rich text format during streaming. |

---

## Background

The Norman server's Phase 4 structured output pattern (`codex-structured-output-pattern.md`) implemented the `RichOutput` struct with:
- `content_type`: High-level category of the output (e.g., "text", "markdown", "html", "json", "pdf", "chart", "diagram", "file_ref")
- `render_hint`: Enum hint for client-side rendering strategy (`text`, `markdown`, `html`, `json`, `pdf`, `chart`, `diagram`, `file_ref`)
- `structured_data`: Machine-readable structured content (JSON object)

The TUI client's assessment (`bubbletea-tui-client-assessment.md`) designed a rendering strategy that maps `render_hint` to specific view components:
- `text` → Plain text block via `lipgloss.NewParagraph()`
- `markdown` → Rendered Markdown via `goldmark` → HTML → terminal-safe text
- `html` → Inline HTML snippet with simple `<br>` replacement + escape
- `json` → Formatted JSON tree with collapsible nodes
- `chart` → ASCII bar chart from `structured_data`
- `diagram` → Mermaid code block with `[diagram]` label
- `file_ref` → File path reference with clickable link indicator
- `pdf` → Download indicator with status bar notification

The server's `pkg/agent/loop.go` has `detectRichOutput()` and `detectContentType()` functions that analyze accumulated task output and populate `RichOutput`:

```go
// detectRichOutput analyzes accumulated task output and returns a RichOutput struct
// populated with content type, render hint, and structured data if parseable as JSON or text/markdown/html.
func (l *Loop) detectRichOutput(output []byte) *protocol.RichOutput {
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) == 0 {
		return nil
	}

	richOut := &protocol.RichOutput{}

	// Detect content type and render hint.
	contentType, renderHint := detectContentType(trimmed)
	richOut.ContentType = strPtr(contentType)
	richOut.RenderHint = &renderHint

	// If output is valid JSON, populate StructuredData.
	var jsonMap map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &jsonMap); err == nil {
		richOut.StructuredData = &jsonMap
	} else if renderHint == protocol.Markdown || renderHint == protocol.Text || renderHint == protocol.Html {
		// For markdown, text, html, populate StructuredData with the content.
		contentMap := map[string]interface{}{
			"content": trimmed,
		}
		richOut.StructuredData = &contentMap
	}

	return richOut
}
```

However, this `RichOutput` data is **not being sent to the client via WebSocket events**. The conversation history notes:
> "Reverted the addition of RichOutput data to WebSocket events (removed richOutputToMap and data field inclusion in emitStored)."

The TUI client currently relies on **client-side format detection** (`detectContentType`, `isJSON`, `hasHTMLTags`, `hasMarkdown` in `cmd/norman/tui/content_detector.go`), but this approach has limitations:
1. Streaming detection trims newlines and fails to properly detect rich text format
2. The final model answer was markdown with a list, but the client printed it as plain text with newlines replaced by spaces

---

## Problem

The TUI client needs to render rich text based on `render_hint` values (docs/design-log/bubbletea-tui-client-assessment.md), but the server does not send `RichOutput` metadata via WebSocket events. The current client-side format detection approach has limitations with streaming content and newline preservation.

The gap: **the WebSocket event schema (`WebSocketEvent` in `api-spec.yaml`) does not include a `rich_output` field**, and the event emission code in `pkg/agent/loop.go` does not include `RichOutput` data in the WebSocket events sent to the client.

---

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `RichOutput` struct (Phase 4, implemented) | Solves: provides typed metadata (`render_hint`, `structured_data`) that a TUI renderer can dispatch to specific view components. |
| `detectRichOutput()` in `pkg/agent/loop.go` | Solves: server-side logic to analyze output and populate `RichOutput` with content type, render hint, and structured data. |
| `detectContentType()`, `isJSON()`, `hasHTMLTags()`, `hasMarkdown()` in `pkg/agent/loop.go` | Solves: content detection logic used by `detectRichOutput()`. |
| `WebSocketEvent` schema in `api-spec.yaml` | Constrains: currently does not include a `rich_output` field. |
| `emitStored()` in `pkg/agent/loop.go` | Constrains: broadcasts WebSocket events but does not include `RichOutput` data. |
| Client-side detection in `cmd/norman/tui/content_detector.go` | Solves: TUI has `detectContentType`, `isJSON`, `hasHTMLTags`, `hasMarkdown`, `isProgressMessage` functions. |
| `renderRichOutput()` dispatcher in TUI design log | Solves: maps `render_hint` to specific view components (JSON tree, ASCII bar chart, Markdown renderer, etc.). |

---

## Questions & Answers

| Q | A |
|---|---|
| **Q1: Should the server send `RichOutput` with streaming chunks or only at completion?** | Only at completion (`turn_completed` and `task_complete` events). Streaming detection on the server is complex and the current client-side streaming detection has newline preservation issues. The `RichOutput` analysis requires the full accumulated output to be accurate. |
| **Q2: How do we add `rich_output` to `WebSocketEvent` without breaking existing clients?** | Add `rich_output` as a nullable field with `$ref: "#/components/schemas/RichOutput"`. Existing clients that don't expect this field will simply ignore it. |
| **Q3: What events should include `rich_output`?** | `turn_completed` and `task_complete` events, as these are the events where the final output is complete and `RichOutput` can be accurately detected. |
| **Q4: Should we keep client-side format detection or replace it with server-generated hints?** | Keep both for now. Server-generated hints provide accurate final rendering, while client-side detection can serve as a fallback for streaming chunks before `turn_completed`. |

---

## Design

### 10.1 API Spec Changes (`api-spec.yaml`)

Add `rich_output` field to the `WebSocketEvent` schema:

```yaml
    WebSocketEvent:
      type: object
      required: [event]
      properties:
        event:
          type: string
          enum:
            - task_progress
            - thinking_progress
            - agent_step
            - task_complete
            - task_failed
            - notification
            - session_event
            - session_compressed
            - context_budget_warning
            - user_response
            - item_started
            - item_completed
            - turn_completed
            - turn_failed
        task_id:
          type: string
          nullable: true
        session_id:
          type: string
          nullable: true
        message:
          type: string
          nullable: true
        step:
          type: integer
          nullable: true
        channel:
          type: string
          nullable: true
        priority:
          type: string
          enum: [normal, high]
          nullable: true
        used_tokens:
          type: integer
          nullable: true
        remaining_tokens:
          type: integer
          nullable: true
        threshold:
          type: string
          nullable: true
          description: Budget threshold that triggered the event (e.g., "75%")
        rich_output:
          $ref: "#/components/schemas/RichOutput"
          nullable: true
          description: Structured metadata about the output (content type, render hint, structured data)
```

### 10.2 Code Generation

Run `make generate` to regenerate the protocol types and WebSocket event handling code from `api-spec.yaml`. This will add the `RichOutput` field to the `WebSocketEvent` struct in the generated `pkg/protocol/types.go` (or equivalent generated file).

### 10.3 Server Event Emission Changes (`pkg/agent/loop.go`)

Update the event emission to include `RichOutput` data in the `turn_completed` and `task_complete` events:

```go
// emitStoredWithRichOutput records a granular event in the task's event log 
// and broadcasts to scope=events clients, including RichOutput data if available.
func (l *Loop) emitStoredWithRichOutput(taskID string, evt protocol.WebSocketEvent, richOut *protocol.RichOutput) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if task, ok := l.tasks[taskID]; ok {
		task.events = append(task.events, evt)
	}
	
	// Include rich_output in the event if available
	if richOut != nil {
		evt.RichOutput = richOut
	}
	
	l.wsHub.BroadcastGranular(evt.Event, taskID, *evt.SessionId, *evt.Message)
}
```

And update the event emissions at task completion:

```go
// Granular event: turn.completed (stored + pushed via scope=events WebSocket clients)
l.emitStoredWithRichOutput(task.ID, protocol.WebSocketEvent{
	Event:     protocol.TurnCompleted,
	TaskId:    &task.ID,
	SessionId: &task.SessionId,
	Message:   strPtr(fmt.Sprintf("turn %d/%d completed with %d bytes output", turn+1, l.config.System.MaxTurns, len(task.Output))),
}, task.RichOutput)
```

And for task completion:

```go
// Granular event: task_complete (stored + pushed via scope=events WebSocket clients)
l.emitStoredWithRichOutput(task.ID, protocol.WebSocketEvent{
	Event:     protocol.TaskComplete,
	TaskId:    &task.ID,
	SessionId: &task.SessionId,
	Message:   strPtr(fmt.Sprintf("task %s completed with status %s", task.ID, task.Status)),
}, task.RichOutput)
```

### 10.4 TUI Client Changes (`cmd/norman/tui/`)

Update the TUI client's WebSocket event handler (`cmd/norman/tui/model.go` or `cmd/norman-tui/websocket.go`) to:
1. Parse the `rich_output` field from WebSocket events
2. Use the `render_hint` to determine how to render the content
3. Use the `structured_data` to render the appropriate format (JSON tree, ASCII chart, Markdown, etc.)

The existing TUI rendering logic in `cmd/norman/tui/renderer.go`, `cmd/norman/tui/json_renderer.go`, `cmd/norman/tui/chart_renderer.go`, and `cmd/norman/tui/markdown_renderer.go` should be used to render the `structured_data` based on the `render_hint`.

---

## Implementation Plan

### Phase 1 — Update API Spec and Regenerate Code (Week 1)

| Step | File | Description |
|------|------|-------------|
| 1 | `api-spec.yaml` | Add `rich_output` field to `WebSocketEvent` schema |
| 2 | `make generate` | Regenerate protocol types and WebSocket event handling code |

**Deliverable:** `WebSocketEvent` struct in generated code includes `RichOutput` field.

### Phase 2 — Update Server Event Emission (Week 2)

| Step | File | Description |
|------|------|-------------|
| 3 | `pkg/agent/loop.go` | Add `emitStoredWithRichOutput()` function to include `RichOutput` in events |
| 4 | `pkg/agent/loop.go` | Update `turn_completed` event emission to use `emitStoredWithRichOutput()` |
| 5 | `pkg/agent/loop.go` | Update `task_complete` event emission to use `emitStoredWithRichOutput()` |

**Deliverable:** Server includes `RichOutput` data in `turn_completed` and `task_complete` WebSocket events.

### Phase 3 — Update TUI Client Rendering (Week 3)

| Step | File | Description |
|------|------|-------------|
| 6 | `cmd/norman/tui/model.go` | Update WebSocket event handler to parse `rich_output` field |
| 7 | `cmd/norman/tui/renderer.go` | Use `render_hint` and `structured_data` from `rich_output` to render content |
| 8 | `cmd/norman/tui/chat_panel.go` | Update chat panel to use rendered rich content instead of plain text |

**Deliverable:** TUI client renders rich content (JSON tree, ASCII chart, Markdown) based on server-generated `render_hint`.

---

## Examples

### ✅ Good: Including `rich_output` in WebSocket event

```yaml
# WebSocket event with rich_output
{
  "event": "turn_completed",
  "task_id": "abc12345",
  "session_id": "6d471374-session",
  "message": "turn 1/1 completed with 420 bytes output",
  "rich_output": {
    "content_type": "markdown",
    "mime_type": "text/markdown",
    "render_hint": "markdown",
    "structured_data": {
      "content": "# Sample Markdown\n* First list item\n* Second list item"
    }
  }
}
```

### ✅ Good: TUI client rendering based on `render_hint`

```go
// renderer.go — renderRichOutput dispatches based on hint from WebSocket event
func renderRichOutput(ro *protocol.RichOutput) string {
    if ro == nil || ro.RenderHint == nil {
        return "" // fallback to plain text message
    }
    
    switch *ro.RenderHint {
    case protocol.Json:
        return renderJSONTree(ro.StructuredData)
    case protocol.Chart:
        return renderBarChart(ro.StructuredData)
    case protocol.Markdown:
        return renderMarkdown(ro.StructuredData)
    case protocol.Text:
        fallthrough
    default:
        if ro.StructuredData != nil {
            if contentMap, ok := (*ro.StructuredData).(map[string]interface{}); ok {
                if content, ok := contentMap["content"].(string); ok {
                    return renderPlainText(content)
                }
            }
        }
        return "" // fallback to plain text message
    }
}
```

### ❌ Bad: Sending `rich_output` with streaming `task_progress` events

```yaml
# BAD — rich_output requires full output to detect accurately
{
  "event": "task_progress",
  "task_id": "abc12345",
  "message": "# Sam",
  "rich_output": {
    "render_hint": "markdown" // incomplete detection
  }
}
```

---

## Trade-offs

| Trade-off | Detail |
|-----------|--------|
| **Server computation** | `detectRichOutput()` runs on the server at task completion. Acceptable overhead as it only runs once per task. |
| **WebSocket event size** | Including `rich_output` in `WebSocketEvent` increases the event payload size, but `structured_data` is only included for JSON/markdown/html/text, which is reasonable. |
| **Client compatibility** | Existing CLI clients that don't expect `rich_output` will simply ignore the nullable field. No breaking changes. |
| **Streaming vs completion** | Server-generated `rich_output` is only available at `turn_completed`/`task_complete`, not during streaming. Client-side detection can still serve as a fallback for streaming chunks. |
| **Accuracy** | Server-generated hints are more accurate because they analyze the full accumulated output, whereas client-side streaming detection has newline preservation issues and may misidentify content type. |

---

## Related Design Decisions to Track

| Decision | Location | Status |
|----------|----------|--------|
| RichOutput schema (Phase 4) | `codex-structured-output-pattern.md` | ✅ Implemented |
| TUI client rendering hints | `bubbletea-tui-client-assessment.md` | ✅ Designed |
| Client-side format detection | `cmd/norman/tui/content_detector.go` | ✅ Implemented (but has limitations) |
| Server `detectRichOutput()` | `pkg/agent/loop.go` | ✅ Implemented (but not sent via WebSocket) |

---

*This is a proposal document. Implementation should follow the phased plan above.*
