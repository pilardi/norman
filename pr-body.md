## PR Description

### Summary
This PR implements session idle timeout configuration with a default of 30 minutes (or 'never' to disable expiration), fixes the `updated_at` timestamp tracking for session idle cleanup, and includes various bug fixes for session compression, TUI error handling, and text wrapping.

### API & Configuration Changes
- Added `session_idle_timeout` to `SystemConfig` schema in `api-spec.yaml`
- Added `session_idle_timeout: "30m"` under `system` configuration in `pkg/config/config_defaults.yaml`
- Added `SessionIdleTimeout string` field to `SystemConfig` struct in `pkg/config/config.go` with default application in `LoadDefault()`
- Regenerated `pkg/protocol/server.gen.go` to include `SessionIdleTimeout *string` in `SystemConfig`

### Session Idle Cleanup Improvements
- Modified `idleCleanupLoop` in `cmd/norman-server/sessions.go` to use `updated_at` instead of `created_at` for checking idle time
- Updated `cmd/norman-server/main.go` to parse `session_idle_timeout` from config with default 30m using `time.ParseDuration`, and pass `idleTimeout` to `srv.idleCleanupLoop(idleTimeout)`
- Added support for `session_idle_timeout=never` to disable session expiration entirely
- Updated `cmd/norman/config.go` to expose and allow setting `session_idle_timeout` via `./dist/norman config --set session_idle_timeout=<value>` and `./dist/norman config --json`
- Added `session_idle_timeout` handling in `cmd/norman-server/config.go` config update endpoint with validation for valid duration or 'never', and greater than 0

### Session Persistence Fix
- Modified `PersistSessionTurn` in `pkg/memory/store.go` to update the session's `updated_at` timestamp in the session JSON file after appending a turn to the `.jsonl` file, ensuring the `idleCleanupLoop` correctly identifies idle sessions based on the last activity time

### Session Compression Features
- Added session compress command to the API (`/api/sessions/{id}/compress` POST endpoint)
- Exposed the compress command in both TUI and REPL clients using shared logic under the client package
- Exposed the compression in the CLI following the other command pattern
- Fixed issue where compressing an empty session causes a 500 error
- Fixed issue where LLM compression fails with 'At least one non-system message is required.'

### TUI & CLI Bug Fixes
- Removed the 'New session 'b226c47f-session' created' printed message at the bottom of the border in the chat panel when a new session is created in the TUI
- Fixed TUI exiting with an error when trying to create a new session and the workspace has reached `max_active_sessions` (3). It now updates the status bar with the error instead of exiting
- Fixed text wrapping issue where error messages print in multiple lines when there's plenty of space to the right (e.g., workspace IDs with hyphens like 'a1b2c3d4-e5f6-7890-abcd-ef1234567890')
- Fixed the `wrapStyledLine` function in `cmd/norman/tui/markdown_renderer.go` to avoid breaking on hyphenated UUIDs or workspace IDs
- Fixed build failure: Linting error 'cmd/norman/slash_commands.go:32:3: undefined: listEmergentTasks' by changing to `nlistEmergentTasks`

### Testing
- Build and lint pass successfully with 0 issues
- Configuration validation properly rejects invalid durations (e.g., "pizza") and non-positive values (e.g., "0", "-5m")
- "never" value properly disables the idle cleanup loop