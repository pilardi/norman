---
title: "Client-Side Markdown and Code Block Rendering for TUI Client"
type: decision_log
status: implemented
date: 2026-11-15
author: pablo (via tool)
related: [bubbletea-tui-client-assessment.md, codex-structured-output-pattern.md]
---

# Client-Side Markdown and Code Block Rendering for TUI Client

## TL;DR

| Decision | Rationale |
|----------|-----------|
| **TUI handles markdown and code block rendering on the client side** | The TUI client uses `renderMarkdownString()` and `renderMarkdownText()` with an `inCodeBlock` state machine to properly render markdown content, including applying a dark background style to code blocks using `lipgloss.NewStyle().Background(lipgloss.Color("#1e1e2e"))`. |
| **No server-generated `RichOutput` metadata sent via WebSocket** | The initial plan to have the server generate `RichOutput` with `render_hint` and `structured_data` and send it via WebSocket events was simplified. The client-side approach covers both streaming and history rendering without requiring server-side changes or WebSocket event schema modifications. |
| **State machine for multi-line code block detection** | The `renderMarkdownText()` function in `cmd/norman/tui/markdown_renderer.go` uses a state machine (`inCodeBlock`, `codeBlockContent`) to properly detect and style multi-line code blocks, preserving newlines and formatting. |

---

## Background

The TUI client needed to properly render markdown content, especially multi-line code blocks like:

```markdown
```json
[1, 42, 7]
```
```

Initially, the TUI was rendering this as plain text:
```
│[13:45:57] Assistant: ... ```json ... [1, 2, 3] ... ```
```

An initial proposal was to have the server generate `RichOutput` metadata with `render_hint` and `structured_data`, and include it in WebSocket events (`turn_completed` and `task_complete`). However, this approach had limitations:
1. It would only cover completion events, not streaming workflow or history rendering.
2. It required modifying the `WebSocketEvent` schema in `api-spec.yaml` and regenerating code.
3. It added complexity to the server-side event emission (`emitStoredWithRichOutput`, `pushCompletion`).

The final approach simplified the solution from the client side: the TUI handles the detection and rendering of markdown with code blocks on its own based on the content it receives, for both streaming and history rendering.

---

## Problem

The TUI client was rendering markdown content with code blocks as plain text, instead of applying proper styling (e.g., dark background for code blocks). The client needed a way to detect and render markdown code blocks properly without relying on server-generated `RichOutput` metadata.

---

## Solution: Client-Side Markdown Rendering

The solution implements client-side markdown rendering using the `renderMarkdownString()` and `renderMarkdownText()` functions in `cmd/norman/tui/markdown_renderer.go`.

### State Machine for Code Block Detection

The `renderMarkdownText()` function uses a state machine to detect and handle multi-line code blocks:

```go
func renderMarkdownString(content string) string {
	var lines []string
	renderMarkdownText(content, &lines)
	return strings.Join(lines, "\n")
}

func renderMarkdownText(content string, lines *[]string) {
	linesSlice := strings.Split(content, "\n")
	inCodeBlock := false
	codeBlockContent := []string{}
	for _, line := range linesSlice {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "```") {
			if !inCodeBlock {
				inCodeBlock = true
				codeBlockContent = []string{}
			} else {
				inCodeBlock = false
				codeBlockStr := strings.Join(codeBlockContent, "\n")
				style := lipgloss.NewStyle().Background(lipgloss.Color("#1e1e2e"))
				renderedCode := "  " + codeBlockStr
				*lines = append(*lines, style.Render(renderedCode))
			}
			continue
		}
		if inCodeBlock {
			codeBlockContent = append(codeBlockContent, line)
			continue
		}
		// ... regular line processing ...
	}
}
```

### Key Features:
1. **`inCodeBlock` state**: Tracks whether the current line is inside a code block.
2. **`codeBlockContent` array**: Accumulates lines within a code block.
3. **Dark background styling**: Uses `lipgloss.NewStyle().Background(lipgloss.Color("#1e1e2e"))` to style code blocks.
4. **Preserves newlines and formatting**: The state machine properly handles multi-line content without trimming or losing formatting.

---

## Implementation Details

### Files Modified:

| File | Description |
|------|-------------|
| `cmd/norman/tui/markdown_renderer.go` | Added `renderMarkdownString()` function and implemented `renderMarkdownText()` with `inCodeBlock` state machine. |
| `cmd/norman/tui/chat_panel.go` | Updated to use `renderMarkdownString(msg.Content)` for rendering content in history view. |
| `cmd/norman/tui/model.go` | Removed `RichOutput` field from `ChatMessage` struct, removed `extractRichOutput` function, removed `protocol` import. |
| `cmd/norman/tui/websocket.go` | Removed `RichOutput` struct and parsing logic. |
| `cmd/norman/tui/renderer.go` | Removed as it was unused after simplifying to client-side rendering. |
| `cmd/norman/tui/chart_renderer.go` | Removed due to linting errors for unused code. |

### Chat Panel Integration:

In `chat_panel.go`, the history rendering now uses `renderMarkdownString()`:

```go
// Then, render regular messages
for _, msg := range p.messages {
	speaker := "Assistant"
	if msg.Role == "user" {
		speaker = "You"
	}
	ts := msg.Timestamp.Format("15:04:05")
	lines = append(lines, "["+ts+"] "+speaker+":")

	// Render content using markdown renderer to properly handle code blocks
	renderedContent := renderMarkdownString(msg.Content)
	lines = append(lines, renderedContent)
	lines = append(lines, "")
}
```

---

## Examples

### ✅ Good: Markdown with JSON code block

**Input:**
```markdown
Here is a JSON array with 3 numbers:

```json
[1, 42, 7]
```
```

**Output (TUI rendered):**
```
[19:44:23] Assistant:
Here is a JSON array with 3 numbers:

  [1, 42, 7]
```
(with dark background `#1e1e2e` on the code block lines)
```

### ✅ Good: Streaming markdown content

The `renderMarkdownString()` function works for both streaming and history rendering, preserving the `inCodeBlock` state across chunks when the chat panel processes the content.

---

## Trade-offs

| Trade-off | Detail |
|-----------|--------|
| **Client-side vs Server-side** | Client-side rendering is simpler and covers both streaming and history rendering without requiring server-side changes or WebSocket schema modifications. |
| **Accuracy** | Client-side detection is accurate for markdown and code blocks because it processes the full content line-by-line with a state machine, preserving newlines and formatting. |
| **Extensibility** | The client-side approach can be extended to support other formats (HTML, JSON tree, ASCII charts) by adding more detection and rendering functions to `markdown_renderer.go` or `content_detector.go`. |
| **Simplicity** | Removing server-side `RichOutput` generation and WebSocket event `rich_output` field handling simplified the codebase and eliminated the need for `emitStoredWithRichOutput` and `BroadcastGranularWithEvent` functions. |

---

## Related Design Decisions to Track

| Decision | Location | Status |
|----------|----------|--------|
| RichOutput schema (Phase 4) | `codex-structured-output-pattern.md` | ✅ Implemented (server-side struct exists, but not sent via WebSocket) |
| TUI client rendering hints | `bubbletea-tui-client-assessment.md` | ✅ Designed (client-side approach implemented instead) |
| Client-side markdown rendering | `cmd/norman/tui/markdown_renderer.go` | ✅ Implemented |
| `inCodeBlock` state machine | `cmd/norman/tui/markdown_renderer.go` | ✅ Implemented |

---

*This document reflects the final implemented approach: client-side markdown and code block rendering using the `renderMarkdownString()` and `renderMarkdownText()` functions with the `inCodeBlock` state machine.*
