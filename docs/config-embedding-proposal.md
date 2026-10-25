# Proposal: Embedded Default Configuration & Help Message Consistency

> **API Spec**: [Full Reference](api-reference.html)


## Problem Statement

Currently, Norman's configuration has a two-tier model that creates friction:

1. **Compiled defaults** are hardcoded as Go constants (`DefaultModel`, `DefaultServerPort`, etc.) in `pkg/config/config.go`.
2. **User-overridable config** lives at `~/.norman/config.yaml` (loaded by `LoadDefault()`).
3. The CLI client and server each have their own help text, leading to drift between what the CLI prints and what the server prints.

The desired behavior is:

- A single canonical `config.yaml` shipped alongside `main.go` (server) defines **defaults** that are baked into the compiled binary.
- When no `.norman/config.yaml` exists in the server's environment, those embedded defaults are used — they are **not user-modifiable**.
- When a user creates/edits `~/.norman/config.yaml`, it becomes an **overlay** that can override the embedded defaults (user-modifiable).
- The help message for both server and client is generated from a single source of truth.

---

## Proposed Architecture

### 1. Embedded Default Config (`config_defaults.yaml`)

```
cmd/norman-server/config_defaults.yaml   ← shipped alongside main.go, compiled into binary
pkg/config/embedded.go                     ← Go code that reads the YAML at build time
```

The file defines the *system defaults* — values that are immutable unless you recompile. Example:

```yaml
# config_defaults.yaml
system:
  default_model: "gpt-4o"
  server_port: 8080
  host: "127.0.0.1"
  temperature: 0.7
  max_tokens: 32000
  skills:
    discovery_paths:
      - "~/.norman/skills/"
      - "/usr/share/norman/skills/"
    max_active: 3
    path: "~/.norman/skills/"
  notify:
    bell_enabled: true
    rate_limit_per_minute: 10
  logging:
    verbose: false
  llm_providers: []
  default_provider: ""
```

This YAML is read at build time (via `go:embed`) and used as the fallback when no user config exists.

### 2. Runtime Config Resolution Order

```
Priority (highest → lowest):
  1. CLI flags / env vars (--provider, NORMAN_HOST, etc.)
  2. ~/.norman/config.yaml          ← user overlay (modifiable)
  3. Embedded defaults from config_defaults.yaml  ← compiled-in fallback
```

The `Load()` function in `pkg/config` would change:

```go
// LoadDefault reads the user config; if absent, falls back to embedded defaults.
func LoadDefault() (*Config, error) {
    path := filepath.Join(os.Getenv("HOME"), ".norman", "config.yaml")
    
    // Try user overlay first
    data, err := os.ReadFile(path)
    if err == nil {
        c := DefaultConfig()
        yaml.Unmarshal(data, c)
        return c, nil  // user config found — done
    }
    
    // No user config → use embedded defaults
    return EmbeddedDefaults(), nil
}
```

### 3. Help Message Unification

The help text currently appears in two places:

| Location | Content |
|----------|---------|
| `cmd/norman-server/main.go` (`--help`) | Server flags, env vars, provider info |
| `cmd/norman/main.go` (`printUsage()`) | CLI commands, flags |

**Proposal**: Move both help texts into a shared function in `pkg/config/help.go`, driven by the embedded defaults. This ensures that if a default changes (e.g., port → 9090), both help messages update automatically.

```go
// pkg/config/help.go
func ServerHelpText() string { ... }   // used by server main
func ClientHelpText() string { ... }   // used by CLI main
```

The text templates would reference constants/defaults rather than hardcoding values like `8080` or `gpt-4o`.

### 4. Migration Path

| Phase | Action |
|-------|--------|
| 1 | Create `config_defaults.yaml` with current hardcoded defaults |
| 2 | Add `go:embed` directive in `pkg/config/embedded.go` to load it at build time |
| 3 | Refactor `LoadDefault()` to prefer user config, fall back to embedded |
| 4 | Extract help text into shared templates referencing embedded values |
| 5 | Update server and CLI main.go to call the shared help functions |
| 6 | Add tests for: (a) no-user-config → embedded defaults used; (b) user-config present → overrides embedded; (c) help text consistency |

---

## Design Decisions & Trade-offs

### Why not just hardcode Go constants in `main.go`?

Hardcoded literals (`const DefaultModel = "gpt-4o"`) are also compiled into the binary and read-only at runtime — so both approaches achieve immutability. The difference is *where* the definition lives:

- **Go constants** are scattered across `.go` files as source code, mixed with logic. A non-developer can't easily audit what's baked in without reading Go syntax.
- **Embedded YAML** is a standalone data file referenced by `embed.FS`. It's plain text, readable by anyone, and clearly separated from loading logic.

This matters because your goal is to *remove defaults from the code* while keeping them post-compilation immutable. `go:embed` achieves both: no Go literals remain in `main.go`, and the YAML is baked into the binary at build time (not a user-editable file on disk).

### What about "not user modifiable"?

The proposal distinguishes two modes:
- **Embedded defaults**: Read-only, baked into binary. Used when `~/.norman/config.yaml` is absent.
- **User overlay**: When the file exists, it *replaces* embedded defaults (full merge). User can edit freely.

This means "not user modifiable" applies only to the *absence* case — if the user wants to customize, they create the file and it becomes modifiable.

### Consistency guarantee

Help messages are now single-source:
```go
const defaultPort = 8080  // in config_defaults.yaml + embedded.go
// Both server and CLI templates reference `defaultPort`, not "8080" literally.
```

---

## File Changes Summary

| File | Change |
|------|--------|
| `pkg/config/config_defaults.yaml` (new) | Canonical defaults YAML, compiled into binary via go:embed |
| `pkg/config/embedded.go` (new) | `go:embed` directive + `EmbeddedDefaults()` function |
| `pkg/config/config.go` (modify) | `LoadDefault()` falls back to embedded; remove hardcoded constants |
| `pkg/config/help.go` (new) | Shared help text templates driven by embedded defaults |
| `cmd/norman-server/main.go` (modify) | Use `config.ServerHelpText()` instead of inline string |
| `cmd/norman/main.go` (modify) | Use `config.ClientHelpText()` instead of inline string |

---

## Testing Plan

1. **Unit tests** in `pkg/config/embedded_test.go`: verify embedded YAML parses into `Config`.
2. **Integration test**: simulate no-user-config → verify defaults match embedded.
3. **Integration test**: simulate user config present → verify merge behavior.
4. **Consistency check**: diff server help vs. CLI help output for structural parity (same flags, same defaults listed).

---

## Open Questions

1. Should `config_defaults.yaml` be git-tracked or generated at build time? → *Recommend: git-tracked for reproducibility.*
2. Should embedded defaults support the full `Config` schema (including providers) or just system-level fields? → *Full schema to avoid split logic.*
3. How do we handle backward compatibility with existing `~/.norman/config.yaml` files that may use different field names? → *YAML merge semantics: user file fully replaces embedded defaults, so no partial-merge risk.*

---

*Draft — v0.1 | 2026-10-22*
