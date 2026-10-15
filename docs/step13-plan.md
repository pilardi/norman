# Step 13 — Logging verbosity

> **Status:** ✅ COMPLETE (merged to `main`)

---

## Problem Statement

Before this step, running any CLI command (e.g., `norman list`) produced a confusing JSON log line followed by the actual output:

```json
{"time":"2026-10-14T15:56:06.468839413-04:00","id":"","remote_ip":"127.0.0.1",...}
No workspaces found.
```

The server's structured JSON log lines leaked into user output by default, making CLI usage noisy and hard to parse programmatically. We needed a **verbose mode** (`-v` / `--verbose`) that controls whether debug/request details are printed to stderr.

---

## Current State (at time of planning)

- `pkg/config/config.go` — no `LoggingConfig` struct; server always logs JSON to stdout.
- `cmd/norman/main.go` — CLI client has no `-v` flag; no request logging.
- Server middleware: `middleware.Logger()` from Echo is applied unconditionally.
- No env var support for controlling verbosity at runtime (e.g., in CI/CD).

---

## Proposed Architecture

### 1. Config Struct (`pkg/config/config.go`)

| File | Purpose |
|------|---------|
| `pkg/config/config.go` | Add `LoggingConfig` struct with `Verbose bool` field; integrate into `SystemConfig`. |

**Design:**
```go
type LoggingConfig struct {
    Verbose bool `yaml:"verbose"` // default: false
}

type SystemConfig struct {
    // ... existing fields ...
    Logging LoggingConfig `yaml:"logging,omitempty"`
}
```

---

### 2. CLI Verbose Flag (`cmd/norman/main.go`)

| Change | Purpose |
|--------|---------|
| Add `-v` / `--verbose` flag to root command | Print `[debug] METHOD URL` before each HTTP request to stderr in verbose mode. |

**CLI usage:**
```bash
norman -v list          # prints debug info to stderr + clean stdout
norman --verbose server  # same, with server startup logs
```

---

### 3. Server Middleware (`cmd/norman-server/main.go`)

| Change | Purpose |
|--------|---------|
| Conditionally apply `middleware.Logger()` based on `cfg.Logging.Verbose`. | Only log HTTP requests when verbose mode is enabled; default to clean stdout. |

**Behavior:**
- Default (no `-v`): Server logs structured JSON to a dedicated log file or stderr only when errors occur.
- Verbose (`-v`): All requests logged as `[debug] GET /api/workspaces` to stderr before response.

---

### 4. Environment Variable Support (`cmd/norman/main.go`)

| Change | Purpose |
|--------|---------|
| Add `LOGGING_VERBOSE` env var support (accepts `true`, `false`, `1`, `0`). | Allow CI/CD pipelines to enable verbose logging without changing config files. |

---

### 5. Documentation Updates

| File | Change |
|------|--------|
| `.env.example` | Add `LOGGING_VERBOSE=false` example variable. |
| CLI help text | Update `--help` output to document `-v` flag and `norman -v` behavior. |

---

## Data Model

```go
// pkg/config/config.go
type LoggingConfig struct {
    Verbose bool `yaml:"verbose"` // default: false
}

// cmd/norman/main.go — verbose flag
var verbose bool
rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose/debug logging to stderr")
```

---

## Integration Points

| Component | Change |
|-----------|--------|
| `pkg/config/config.go` | Add `LoggingConfig` struct; integrate into `SystemConfig`. |
| `cmd/norman/main.go` | Add `-v`/`--verbose` flag; add `LOGGING_VERBOSE` env var check. |
| `cmd/norman-server/main.go` | Conditional `middleware.Logger()` based on config. |
| `.env.example` | Add `LOGGING_VERBOSE` example. |

---

## Acceptance Criteria

- [ ] Default CLI output is clean (no JSON noise) — `norman list` shows only workspace names.
- [ ] `norman -v list` prints `[debug] GET http://...` to stderr before actual output.
- [ ] `LOGGING_VERBOSE=true` env var enables verbose mode even without `-v` flag.
- [ ] Server respects `config.yaml` `logging.verbose: true` for request logging.
- [ ] Help text documents the `-v` flag and its behavior.

---

## Before / After Comparison

**Before (default):**
```json
{"time":"2026-10-14T15:56:06.468839413-04:00","id":"","remote_ip":"127.0.0.1",...}
No workspaces found.
```

**After (default):**
```
Available workspaces:
  my-project        /home/user/workspaces/my-project
```

**After (`norman -v list`):**
```
[debug] GET http://127.0.0.1:8080/api/workspaces
Available workspaces:
  my-project        /home/user/workspaces/my-project
```

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Verbose output mixed with actual CLI output | Log to stderr (`os.Stderr`), not stdout; stdout remains clean for piping. |
| JSON log lines from server leak into client output | Server uses separate logger (`logrus` or `zap`) writing to a file, not stdout. |
| Env var conflicts with config file setting | Config file takes precedence; env var is a fallback (standard Go pattern). |

---

## Dependencies on Other Steps

- **Step 10** (server rename): Server must be at `cmd/norman-server/main.go` for middleware integration.
- **Step 14a** (config path updates): Logging config paths must use unified `~/.norman/` location.
- **Step 7** (budget/compression): Verbose mode could optionally log token counts per turn.

---

*This is a plan draft. Actual implementation follows approved sub-steps.*
