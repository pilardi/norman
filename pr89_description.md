## Summary

This PR includes improvements across the TUI renderers and the server's `RichOutput` generation to properly handle and render all output types, including multi-line code blocks and structured data for markdown/text/html, and fixes the WebSocket event data transmission for RichOutput.

## Changes

### 1. TUI Renderers - Handle all `render_hint` types
- Added support for `text`, `html`, `pdf`, `diagram`, and `file_ref` render hints in `renderRichOutput`
- Added `renderText`, `renderHtml`, `renderPdf`, `renderDiagram`, and `renderFileRef` functions
- Ensured all `RichOutputRenderHint` enum values are properly handled instead of falling through to the default fallback

### 2. TUI Markdown Renderer - Fix multi-line code block rendering
- Replaced regex-based single-line code block detection with a state machine (`inCodeBlock`, `codeBlockContent`)
- Properly detect and render multi-line code blocks with ``` ... ``` syntax
- Preserve internal indentation inside code blocks
- Removed unused `reCodeBlock` regex

### 3. Server `RichOutput` Generation - Populate `StructuredData` for markdown/text/html
- Modified `detectRichOutput` in `pkg/agent/loop.go` to populate `StructuredData` with `{"content": trimmed}` for `markdown`, `text`, and `html` render hints, instead of only populating it for valid JSON.
- Previously, when `detectContentType` returned `markdown`, `text`, or `html` render_hint, `detectRichOutput` tried to unmarshal the whole string (including the ``` ... ``` wrapper) as JSON, which failed. So `StructuredData` remained `nil`.
- In the TUI's `renderMarkdown` function, it checked if `data == nil || (*data)["content"] == nil`, and returned '(no content)'.
- This fix ensures that markdown code blocks like ```json\n{...}\n``` are properly rendered with content in the TUI instead of '(no content)'.

### 4. WebSocket Events - Include RichOutput data in event's Data field
- Added 'data' field to `WebSocketEvent` schema in `api-spec.yaml` to include RichOutput metadata (`content_type`, `mime_type`, `render_hint`, `artifact_url`, `structured_data`)
- Added `richOutputToMap` helper function to convert `RichOutput` to `map[string]interface{}`
- Modified `emitStored` to include `RichOutput` data in `WebSocketEvent.Data` field
- This fixes the issue where markdown content with code blocks was rendered as plain text with newlines replaced by spaces, because the TUI's `extractRichOutput(msg.Data)` was not receiving the `RichOutput` data and fell back to rendering the raw text with `wrapString`, which replaces newlines with spaces using `strings.Fields()`.

## How the TUI Renderers Work

### How the renderers detect the output needs to be rendered:
1. The backend/agent generates a `RichOutput` object with fields like `content_type`, `mime_type`, `render_hint`, `artifact_url`, and `structured_data`.
2. The `render_hint` field is an enum with values: `text`, `markdown`, `html`, `json`, `pdf`, `chart`, `diagram`, `file_ref`.
3. The TUI client's `extractRichOutput` function extracts the `render_hint` and `structured_data` from the WebSocket event's `data` map.
4. The `renderRichOutput` function dispatches to the appropriate renderer based on the `render_hint`.

### How the model knows how to generate a response in the format the client would recognize:
1. The backend/agent is responsible for generating the `RichOutput` with the appropriate `render_hint` based on the output type.
2. The `render_hint` values are mapped to `protocol` enum values in the server code.
3. The `renderRichOutput` function then dispatches to the appropriate renderer:
   - `text` -> `renderText`
   - `markdown` -> `renderMarkdown`
   - `html` -> `renderHtml`
   - `json` -> `renderJSONTree`
   - `pdf` -> `renderPdf`
   - `chart` -> `renderBarChart`
   - `diagram` -> `renderDiagram`
   - `file_ref` -> `renderFileRef`