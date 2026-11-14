## Summary

This PR fixes the issue where markdown content with code blocks (like ```json ... ```) was rendered as plain text with newlines replaced by spaces.

## Root Cause

The `WebSocketEvent` schema in `api-spec.yaml` didn't include a `data` field for `RichOutput` metadata (`content_type`, `mime_type`, `render_hint`, `artifact_url`, `structured_data`). This meant the TUI's `extractRichOutput(msg.Data)` was not receiving the `RichOutput` data, so it fell back to rendering the raw text with `wrapString`, which replaces newlines with spaces using `strings.Fields()`.

## Changes

1. Added 'data' field to `WebSocketEvent` schema in `api-spec.yaml` to include RichOutput metadata
2. Added `richOutputToMap` helper function to convert `RichOutput` to `map[string]interface{}`
3. Modified `emitStored` to include `RichOutput` data in `WebSocketEvent.Data` field

## Testing

The fix ensures that when the model returns markdown content with code blocks, the TUI will properly render it as markdown with the code block formatted correctly, instead of presenting it as plain text with newlines replaced by spaces.