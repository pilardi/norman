---
title: "Bubbletea TUI Client Assessment for Norman"
type: assessment
status: draft
date: 2026-11-09
author: pablo (via tool)
related: [codex-structured-output-pattern.md, clean-client-plan.md, session-workflow-proposal.md, roger-client-design.md]
---

# Bubbletea TUI Client Assessment for Norman Session Experience

## TL;DR

| Decision | Rationale |
|----------|-----------|
| **Adopt `charmbracelet/bubbletea` as the TUI framework** | Proven Elm-architecture pattern, excellent keyboard/mouse support, mature ecosystem (lipgloss, bubbles, bubbles-style). Low barrier to entry for Go devs. |
| **Target: a *rich client* (`norman-tui`) that runs alongside the existing CLI** | No breaking changes; the current `cmd/norman` REPL remains the fallback. The TUI is an opt-in enhancement layer. |
| **Primary value proposition**: structured rendering of `RichOutput` (charts, diagrams, Markdown tables) + real-time event visualization from granular WebSocket events | Directly leverages Phase 4's `rich_output` schema and `?scope=events` WebSocket subscription — the infrastructure is already in place. |
| **Scope v1**: session chat (real-time streaming), task progress dashboard, slash-command input, workspace switcher | Keep MVP tight: one "primary" view (chat), one "secondary" panel (tasks/events). Don't boil the ocean. |
| **Not in scope for v1**: full provider/workspace management UI, skill installer, config editor | Those belong to the existing CLI or a future `norman-admin` subcommand. |

---

## Background

The Norman client (`cmd/norman/`) is currently a basic REPL: `bufio.Scanner` reads stdin line-by-line, WebSocket events are unmarshalled as `map[string]interface{}` and printed with minimal formatting (`[ws] task_progress  step=3 task=abc12345`). The server-side infrastructure for rich content (Phase 4 from `codex-structured-output-pattern.md`) is already implemented:

- `RichOutput` struct with `render_hint` enum (`text`, `markdown`, `html`, `json`, `pdf`, `chart`, `diagram`, `file_ref`)
- Granular WebSocket events (`item_started`, `item_completed`, `turn_completed`, `turn_failed`)
- REST `/api/tasks/{id}/log?type=events` for pull-based access

The gap: **the client has zero awareness of this rich content**. A user chatting with the agent sees only raw text — no rendered charts, no formatted JSON tables, no Mermaid diagrams inline. The current `displayWSMessage()` and `displayResponseTyped()` functions treat everything as plain text.

This assessment evaluates whether `charmbracelet/bubbletea` is the right framework to close this gap, and what a v1 TUI client would look like.

---

## Problem

The current REPL client has three fundamental limitations for a "rich" session experience:

1. **No structured rendering** — All output flows through `fmt.Println()` → plain text. There is no mechanism to render `RichOutput.render_hint` as an interactive chart, a formatted JSON tree, or rendered Markdown.
2. **No real-time visual feedback** — WebSocket events are printed as `[ws] task_progress  step=3` monospace strings. A TUI could show a progress bar, a scrollable event log, and a status indicator simultaneously.
3. **Single-panel layout** — The REPL is one stream of text. A user cannot see chat messages *and* task progress *and* workspace state in the same view without toggling panels.

These limitations exist because the client architecture is fundamentally linear: input → send → receive text → print. A TUI framework introduces a model-view-update cycle that can maintain multiple concurrent views.

---

## Prior Art / Adjacent Mechanisms

| Existing Item | Solves/Constrains This Problem? |
|---------------|--------------------------------|
| `RichOutput` (Phase 4, implemented) | Solves: provides typed metadata (`render_hint`, `structured_data`) that a TUI renderer can dispatch to specific view components. |
| Granular WebSocket events (`item_started`, `item_completed`, `turn_completed`, `turn_failed`) | Solves: enables real-time event visualization (progress bars, item badges) rather than coarse notifications. |
| `?scope=events` WebSocket subscription | Solves: the infrastructure for granular event push is already wired in `cmd/norman-server/websocket.go`. A TUI client just needs to opt in. |
| `displayResponseTyped()` in `chat.go` | Constrains: currently does `fmt.Println(*result.Response)` — a trivial wrapper around raw text. Needs replacement with structured rendering logic. |
| `runWebSocketLoop(sessionID)` in `chat.go` | Constrains: runs as a goroutine reading JSON maps. A TUI would replace this with a dedicated event handler that feeds the Model's Update loop. |
| `cmd/norman/chat.go` (~200 lines) | Solves: provides the existing chat flow (slash commands, message sending, session lifecycle). The TUI client can reuse most of this logic but wrap it in a TUI view layer. |

### Relevant Go Ecosystem Precedents

| Project | Relevance |
|---------|-----------|
| `charmbracelet/bubbletea` | The TUI framework under assessment — Elm-architecture, terminal-native. |
| `charmbracelet/lipgloss` | CSS-like styling for TUI elements (borders, padding, alignment). Complements bubbletea. |
| `charmbracelet/bubbles` | Reusable components: spinner, textarea, list, help, confirm — exactly what a chat client needs. |
| `github.com/erikgeiser/promptkit` | Alternative TUI library (simpler, fewer features). Considered but rejected due to less mature component ecosystem. |
| `github.com/muesli/reflow` | Text wrapping utilities useful for rendering Markdown/HTML in terminal cells. |

### Why Not Alternatives?

- **`tview`**: More powerful (flexbox layouts, tables, forms) but heavier (~10MB binary), less "Go idiomatic" API, and the community is smaller. For a chat client, bubbletea's component model is a better fit.
- **`charmbracelet/tea`** (predecessor to bubbletea): Deprecated in favor of `bubbletea`. Use `bubbletea` directly.
- **Custom terminal rendering (curses-style)**: Too much boilerplate for what `bubbles` components solve out-of-the-box.

---

## Questions & Answers

| Q | A |
|---|---|
| **Q1: Should the TUI client be a separate binary (`norman-tui`) or an integrated subcommand (`norman tui`)?** | Separate binary (`cmd/norman-tui/`). Keeps dependencies isolated (bubbletea + lipgloss) and allows parallel development. The existing `cmd/norman` stays lightweight for script/automation use. |
| **Q2: How do we handle the WebSocket connection in a TUI context?** | Replace `runWebSocketLoop()`'s goroutine with a dedicated `EventQueue` that feeds parsed events into the Model's Update loop via a `tea.Cmd`. The existing `?scope=events` subscription provides granular data; the default `?session_id=` (coarse) can serve as a fallback. |
| **Q3: Can we render Markdown/HTML in a terminal cell?** | Partially. `muesli/reflow/wrap` handles text wrapping. For full Markdown rendering, we'd need a parser (`github.com/yuin/goldmark`) — but for v1, limit to code blocks, tables (ASCII), and bold/italic via lipgloss styling. Full HTML → terminal is a Phase 2 enhancement. |
| **Q4: What's the minimum viable view set?** | Three views: (1) Chat panel (scrollable message stream + input textarea), (2) Task/Event panel (progress indicators, item badges), (3) Status bar (session ID, workspace name, connection health). This is a "two-panel" layout: chat takes ~70%, events takes ~30%. |
| **Q5: How do slash commands map to keyboard events?** | `/` as the trigger key (like tmux/vim). Pressing `/` switches the input textarea into command mode, where `enter` submits the command. This mirrors the existing REPL behavior but with visual feedback (e.g., `[command] /close` shown in the status bar). |
| **Q6: What about mouse support?** | `bubbletea` supports mouse events natively. For v1: scroll up/down in chat panel, click to switch panels. Full mouse-driven interaction can be a Phase 2 addition. |
| **Q7: Should we render charts inline or as separate artifacts?** | Inline for small data (bar charts via ASCII art using `github.com/charmbracelet/bubbles/spinner`-style rendering). For complex charts (multi-axis, large datasets), set `artifact_url` and show a "download" indicator in the status bar. Keep v1 simple: ASCII bar chart renderer. |

---

## Design

### 7.1 Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    norman-tui (TUI Client)                     │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Model (tea.Model)                                    │   │
│  │  ┌──────────────────┐  ┌──────────────────────────┐  │   │
│  │  │ Chat Panel       │  │ Event/Task Panel          │  │   │
│  │  │ (lipgloss bordered)│  │ (bubbles/spinner + list)│  │   │
│  │  └──────────────────┘  └──────────────────────────┘  │   │
│  │  ┌─────────────────────────────────────────────────┐  │   │
│  │  │ Status Bar (session ID | workspace | connection) │  │   │
│  │  └─────────────────────────────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  Event Handlers:                                             │
│    ├── WebSocket event → Model.Update()                      │
│    ├── Key press (textarea input) → Model.Update()           │
│    └── Mouse click → Model.Update()                         │
│                                                              │
│  Data Sources:                                               │
│    ├── protocol.WebSocketEvent (granular events)             │
│    ├── RichOutput.render_hint → renderer dispatch            │
│    └── Session/Workspace state from API                      │
└─────────────────────────────────────────────────────────────┘
```

### 7.2 Component Breakdown

| Component | Library | Purpose | v1 In Scope? |
|-----------|---------|---------|--------------|
| `bubbletea.Model` | `charmbracelet/bubbletea` | Main event loop (Update → View cycle) | ✅ |
| `bubbles.TextArea` | `charmbracelet/bubbles` | Chat input with multi-line support | ✅ |
| `bubbles.List` | `charmbracelet/bubbles` | Event/task list with filtering | ✅ |
| `lipgloss.Border` | `charmbracelet/lipgloss` | Panel borders, padding, alignment | ✅ |
| `bubbles.Spinner` | `charmbracelet/bubbles` | Loading/progress indicators | ✅ |
| Markdown renderer | `github.com/yuin/goldmark` (Phase 2) | Full Markdown rendering in cells | ❌ (Phase 2) |
| ASCII chart renderer | Custom (v1) → `github.com/schollz/progressbar` (Phase 2) | Inline bar charts from `RichOutput` | ✅ (basic) |

### 7.3 View Layout (v1)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Session: abc12345 │ Workspace: api-migration │ Status: ● Connected         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  [Chat Panel — scrollable]                                                  │
│                                                                             │
│  User: Show me the Q4 metrics.                                            │
│                                                                             │
│  Assistant: Here's a summary of Q4 metrics:                                │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ 📊 Chart (render_hint=chart)                                       │   │
│  │                                                                     │   │
│  │  Revenue    ████████████ 85%                                      │   │
│  │  Tasks      ██████████    72%                                     │   │
│  │  Errors     ██              12%                                   │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ 📋 JSON (render_hint=json)                                         │   │
│  │                                                                     │   │
│  │  { "total_tasks": 42, "completed": 38, ... }                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│  [Event Panel — scrollable]                                                 │
│                                                                             │
│  ● tool_call: shell (started)                                               │
│  ● item_completed: file_change (README.md modified)                          │
│  ● turn_completed (tokens: in=1250 out=890)                                  │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│  > Type a message...                                                        │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 7.4 Data Flow (WebSocket → Model)

```
WebSocket Event Stream
        │
        ▼
  ┌──────────────────┐
  │ EventDispatcher   │ ← parses JSON → protocol.WebSocketEvent
  │ (tea.Cmd)         │
  └──────────────────┘
        │
        ▼
  ┌──────────────────┐
  │ Model.Update()    │ ← switches on event type:
  │                   │   - item_started → append to Event panel
  │                   │   - turn_completed → append to Chat panel
  │                   │   - task_complete → update status bar
  └──────────────────┘
        │
        ▼
  ┌──────────────────┐
  │ Model.View()      │ ← renders:
  │                   │   - ChatPanel (lipgloss bordered)
  │                   │   - EventPanel (bubbles.List)
  │                   │   - StatusBar (simple text)
  └──────────────────┘
```

### 7.5 RichOutput Rendering Strategy

The `RichOutput.render_hint` enum maps to specific view components:

| `render_hint` | Renderer | Component Library |
|---------------|----------|-------------------|
| `text` | Plain text block | `lipgloss.NewParagraph()` |
| `markdown` | Rendered Markdown (Phase 2) | `goldmark` → HTML → terminal-safe text |
| `html` | Inline HTML snippet | Simple `<br>` replacement + escape |
| `json` | Formatted JSON tree | Custom recursive renderer with collapsible nodes |
| `chart` | ASCII bar chart | Custom `renderBarChart()` function |
| `diagram` | Mermaid code block | Show as fenced code block with `[diagram]` label |
| `file_ref` | File path reference | Show as clickable link (Phase 2: mouse-click to open) |
| `pdf` | Download indicator | Status bar notification + `[download]` button |

### 7.6 Slash Commands → Keyboard Events

Current REPL slash-commands map to TUI keyboard shortcuts:

| Command | Key Binding | Behavior |
|---------|-------------|----------|
| `/help` | `Ctrl+h` | Show help overlay (bubbles.Confirm-style modal) |
| `/tasks` | `Ctrl+t` | Focus Event Panel, filter by task events |
| `/close` | `Ctrl+c` | Send close command to session |
| `/new` | `Ctrl+n` | Create new session (auto-create) |
| `/history [N]` | `Ctrl+h` then type N | Show last N messages in Chat Panel |
| `/sessions` | `Ctrl+s` | Session switcher (bubbles.List modal) |
| `/switch <id>` | `Ctrl+w` then type ID | Switch to session by ID |
| `/archive <id>` | `Ctrl+a` then type ID | Archive session transcript |

**Design decision:** Use `Ctrl` + letter bindings (like tmux/vim) rather than typing `/`. The `/` key can be reserved as a "command mode" trigger (toggle between input and command mode), similar to how vim uses `/` for search. This avoids conflicts with messages that naturally contain `/`.

### 7.7 Session Lifecycle in TUI

```
┌──────────────┐     ┌──────────────────┐     ┌─────────────────────┐
│  Workspace    │────▶│  Select Session   │────▶│  Chat Mode (active) │
│  Selection    │     │  (bubbles.List)   │     │                     │
└──────────────┘     └──────────────────┘     └─────────────────────┘
                                                        │
                                                        ▼
                                                 ┌─────────────────┐
                                                 │ Session Closed  │
                                                 │ (auto/archive)  │
                                                 └─────────────────┘
```

- **Phase 1**: Connect → auto-select first available session for the workspace
- **Phase 2**: Allow manual session switching via `Ctrl+s` → bubbles.List modal
- **Phase 3**: Show connection health indicator in status bar (reconnect on WS drop)

---

## Implementation Plan

### Phase 1 — Scaffold & Basic Chat Panel (Week 1-2)

**Goal:** A working TUI that mirrors the existing REPL chat experience but with structured rendering.

| Step | File | Description |
|------|------|-------------|
| 1 | `cmd/norman-tui/main.go` | Entry point: `tea.NewProgram(model, tea.WithAltScreen())` |
| 2 | `cmd/norman-tui/model.go` | Model struct with ChatPanel, EventPanel, StatusBar fields |
| 3 | `cmd/norman-tui/chat_panel.go` | Chat panel rendering (scrollable text area) |
| 4 | `cmd/norman-tui/event_panel.go` | Event panel using bubbles.List |
| 5 | `cmd/norman-tui/status_bar.go` | Status bar with session ID, workspace, connection state |
| 6 | `cmd/norman-tui/websocket.go` | WebSocket connection + event dispatch to Model.Update() |

**Deliverable:** A TUI client that connects to the server, shows chat messages in a bordered panel, and displays a basic status bar.

### Phase 2 — RichOutput Rendering (Week 3-4)

| Step | File | Description |
|------|------|-------------|
| 7 | `cmd/norman-tui/renderer.go` | `renderRichOutput(RichOutput)` dispatcher function |
| 8 | `cmd/norman-tui/json_renderer.go` | JSON tree renderer with collapsible nodes |
| 9 | `cmd/norman-tui/chart_renderer.go` | ASCII bar chart from structured_data |
| 10 | `cmd/norman-tui/markdown_renderer.go` | Basic Markdown → terminal text (Phase 2) |

**Deliverable:** Chat messages with `render_hint=chart`, `render_hint=json`, `render_hint=markdown` render as formatted content instead of raw text.

### Phase 3 — Keyboard Commands & Session Management (Week 5)

| Step | File | Description |
|------|------|-------------|
| 11 | `cmd/norman-tui/commands.go` | Slash-command → key binding mapping + command execution |
| 12 | `cmd/norman-tui/session_switcher.go` | bubbles.List modal for session switching |
| 13 | `cmd/norman-tui/help_overlay.go` | Help overlay (bubbles.Confirm-style) |

**Deliverable:** Full key bindings matching the existing slash-commands, session switching via TUI modal.

### Phase 4 — Polish & Integration Tests (Week 6)

| Step | File | Description |
|------|------|-------------|
| 14 | `cmd/norman-tui/main_test.go` | Unit tests for renderer functions |
| 15 | `Makefile` | Add `norman-tui` build target |
| 16 | `go.mod` / `go.sum` | Add bubbletea, lipgloss, bubbles dependencies |

**Deliverable:** Clean build, passing tests, documented usage.

---

## Examples

### ✅ Good: Rendering a JSON response as a formatted tree

```go
// renderer.go — renderRichOutput dispatches based on hint
func renderRichOutput(ro *protocol.RichOutput) string {
    switch ro.RenderHint {
    case "json":
        return renderJSONTree(ro.StructuredData)
    case "chart":
        return renderBarChart(ro.StructuredData)
    case "markdown":
        return renderMarkdown(ro.StructuredData)
    default:
        return string(ro.StructuredData) // fallback to plain text
    }
}

// JSON tree renderer (simplified)
func renderJSONTree(data map[string]any) string {
    var lines []string
    lines = append(lines, "┌─ JSON Output ───────────────────────────────────────┐")
    for k, v := range data {
        switch v.(type) {
        case int, float64:
            lines = append(lines, fmt.Sprintf("│  %-20s %v", k+":", v))
        default:
            lines = append(lines, fmt.Sprintf("│  %-20s [%T]", k+":", v))
        }
    }
    lines = append(lines, "└─────────────────────────────────────────────────────┘")
    return strings.Join(lines, "\n")
}
```

### ❌ Bad: Trying to render full HTML in a terminal cell without sanitization

```go
// BAD — no escaping, breaks layout
func renderHTML(html string) string {
    return html // raw HTML can contain newlines, <br>, etc. that break TUI layout
}

// GOOD — convert HTML to terminal-safe text
func renderHTML(html string) string {
    doc := jsoup.NewDocumentFromString(html)
    return doc.Text() // strips tags, keeps text content
}
```

### ✅ Good: WebSocket event → Model.Update mapping

```go
func (m *Model) handleWebSocketEvent(evt protocol.WebSocketEvent) tea.Cmd {
    switch evt.Event {
    case protocol.WebSocketEventItemStarted:
        m.eventPanel.AppendItem(EventItem{
            Type:       "tool_call",
            Message:    evt.Message,
            Timestamp:  time.Now(),
        })
    case protocol.WebSocketEventTurnCompleted:
        m.chatPanel.AppendMessage(Message{
            Role:      "assistant",
            Content:   evt.Message,
            RichOutput: extractRichOutput(evt), // parse from event payload
        })
    case protocol.WebSocketEventTaskComplete:
        m.statusBar.SetConnection("● Connected")
        return nil
    }
    return nil
}
```

---

## Trade-offs

| Trade-off | Detail |
|-----------|--------|
| **Dependency surface** | Adds `bubbletea` (~500KB), `lipgloss` (~100KB), `bubbles` (~200KB) to the build. Acceptable for a dedicated TUI binary; not desirable in the lightweight `norman` CLI. |
| **Learning curve** | Elm-architecture (Model/Update/View) is unfamiliar to most Go devs. Mitigation: provide a minimal "hello world" example in `cmd/norman-tui/main.go`. |
| **Terminal compatibility** | bubbletea requires ANSI terminal support. Falls back gracefully on non-TTY environments (shows help text, exits). No regression for existing CLI users. |
| **Maintenance burden** | TUI code is more complex than a linear REPL (~500+ lines vs ~200). Mitigation: keep views composable; don't over-engineer v1. |
| **Mouse support adds complexity** | Mouse events require tracking cursor position in scrollable panels. Defer to Phase 3 unless user explicitly requests it. |
| **No breaking changes** | The TUI is additive — the existing `cmd/norman` REPL continues to work unchanged. Users can choose their interface. |

---

## Implementation Results

**Status: 🟡 Phase 1 scaffold complete (2026-11-09).**

### Completed (Phase 1 — Scaffold & Basic Chat Panel)

| File | Lines | Status |
|------|-------|--------|
| `cmd/norman-tui/main.go` | ~73 | ✅ Entry point, client init, workspace selection |
| `cmd/norman-tui/model.go` | ~175 | ✅ Model struct, Init/Update/View cycle |
| `cmd/norman-tui/chat_panel.go` | ~94 | ✅ Scrollable chat with textinput + lipgloss borders |
| `cmd/norman-tui/event_panel.go` | ~71 | ✅ Event panel via bubbles.List + RenderEventPanel |
| `cmd/norman-tui/status_bar.go` | ~54 | ✅ Connection state, workspace, session ID display |
| `cmd/norman-tui/websocket.go` | ~165 | ✅ WS connection + event dispatch to Model.Update() |

### Build Status
- `golangci-lint run ./cmd/norman-tui/...` — **0 issues** ✅
- `make build` — binary produced at `dist/norman-tui` (12MB) ✅
- Smoke test: exits gracefully on non-TTY (`tea.WithAltScreen()`) ✅

### Pending (Phases 2–4)
- Phase 2: RichOutput rendering (JSON trees, ASCII bar charts)
- Phase 3: Keyboard commands mapping + session switcher modal
- Phase 4: Polish + integration tests

### Notes for Future Implementation

1. **Start with Phase 1 only** — get a working TUI that mirrors the existing chat experience before adding rich rendering.
2. **Reuse `chat.go` logic** — the WebSocket connection, message sending, and slash-command parsing should be shared (import from `cmd/norman/`) rather than duplicated. Create a `pkg/client/` package for shared client logic.
3. **Test with `tea.WithAltScreen()`** — this is critical for full-screen TUI experience; without it, the terminal flickers badly on resize.
4. **Consider `tea.WithInputTTY()`** — ensures keyboard input works even when piped (for automation scripts that want to capture output).
5. **Keep `RichOutput` rendering simple in v1** — ASCII bar charts and JSON trees are sufficient. Defer full Markdown/HTML rendering to Phase 2+3.

---

## Related Design Decisions to Track

| Decision | Location | Status |
|----------|----------|--------|
| RichOutput schema (Phase 4) | `codex-structured-output-pattern.md` | ✅ Implemented |
| Granular WebSocket events | `codex-structured-output-pattern.md` | ✅ Implemented |
| Session lifecycle | `session-workflow-proposal.md` | ✅ Implemented |
| Clean client API usage | `clean-client-plan.md` | ✅ Implemented |
| Roger CLI (script integration) | `roger-client-design.md` | ✅ Implemented |

---

*This is an assessment document. Implementation should follow the phased plan above.*
