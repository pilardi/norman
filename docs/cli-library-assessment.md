# CLI Library Assessment — Replacing Manual Flag Parsing

> Date: 2026-10-29
> Branch: `feat/cli-library-assessment`
> Status: **Assessment only — no code changes**

---

## 1. Current State

### 1.1 Components and Their CLI Code

| Component | Location | Lines of CLI code | Entry point |
|-----------|----------|-------------------|-------------|
| `norman` (client) | `cmd/norman/` | ~2,428 lines across 8 files | `main.go` + subcommand handlers |
| `norman-server` | `cmd/norman-server/` | ~3,022 lines across 10 files | `main.go` (minimal flags) |
| `roger` | `cmd/roger/main.go` | ~325 lines | Single-file CLI |

### 1.2 Patterns Currently Used

**Manual flag parsing (repeated across all components):**
```go
// Pattern A: linear scan with manual increment
for i := 0; i < len(args); i++ {
    if args[i] == "--flag" && i+1 < len(args) {
        value = args[i+1]
        i++ // skip consumed argument
    }
}

// Pattern B: positional argument extraction (no flags)
name := args[0]
args = args[1:]
```

**Manual help text generation:**
- `pkg/config/help.go` — `ClientHelpText()` and `ServerHelpText()` as concatenated string literals
- Inline `fmt.Println("...")` in each handler file (e.g., `printprovidersHelp()`)
- Hardcoded usage strings with inconsistent formatting

**Manual type conversion:**
```go
tempVal, err := strconv.ParseFloat(args[i+1], 64)
tokensVal, err := strconv.Atoi(args[i+1])
```

### 1.3 Identified Problems

| # | Problem | Example | Impact |
|---|---------|---------|--------|
| P1 | **Inconsistent help formatting** — different components use different styles (some use `fmt.Println`, others raw strings, some indent differently) | `printprovidersHelp()` vs `config.ClientHelpText()` vs roger's heredoc | Poor UX; users see varying conventions |
| P2 | **No flag validation at parse time** — invalid values reach handlers and produce cryptic errors | `"--temp abc"` → `strconv.ParseFloat` fails mid-handler | User must re-run with correct value |
| P3 | **Flag order dependency / no grouping** — flags can appear anywhere but there's no logical grouping (positional vs optional) | `norman providers add --type ollama local --endpoint http://...` works but is confusing | Cognitive load for users |
| P4 | **Repetition of boilerplate** — every subcommand re-implements its own flag loop | `handleProvidersAdd`, `handleChat`, `handleNotify` all have the same pattern | Maintenance burden; bugs propagate |
| P5 | **No built-in help hierarchy** — `norman providers --help` doesn't work (only `norman providers help` or `-h`); subcommand help is separate | No nested help system | Users must know exact command structure |
| P6 | **Error messages are ad-hoc** — no consistent prefix/suffix pattern | `"Error: 'purge' requires..."` vs `"error: --title is required"` vs `"API error: %v"` | Inconsistent user experience |
| P7 | **No auto-completion support** — shell completion would require manual implementation | `norman workspaces create` has no tab-completion | Productivity loss for power users |

---

## 2. Candidate Libraries

### 2.1 urfave/cli (v3)

| Attribute | Details |
|-----------|---------|
| Repo | `github.com/urfave/cli/v3` |
| License | MIT |
| Maturity | Very mature; used by Docker, Kubernetes CLI tooling, many Go projects |
| Version | v3.0+ (modern, with built-in help generation) |

**Strengths:**
- **Automatic help generation** from struct tags — no manual strings needed
- **Nested subcommands** — `norman providers add` maps cleanly to a command tree
- **Built-in validation** — `StringFlag`, `Float64Flag`, `IntFlag` types with auto-parsing and error messages
- **Consistent output** — standardized help format across all commands
- **Shell completion** — built-in Bash/Zsh/Fish completions
- **Low boilerplate** — one struct per command replaces ~20 lines of manual parsing

**Weaknesses:**
- Adds a dependency (~150 KB compiled)
- v3 has breaking changes from v2 (but we're starting fresh, so irrelevant)
- Slightly opinionated on help text layout (customizable but not trivial to override)

**Migration effort estimate:** ~1–2 weeks for full replacement across all three components.

### 2.2 spf13/cobra + pflag

| Attribute | Details |
|-----------|---------|
| Repo | `github.com/spf13/cobra` + `github.com/spf13/pflag` |
| License | Apache 2.0 |
| Maturity | Extremely mature; used by Kubernetes, Hugo, Terraform CLI, many projects |
| Version | v1.9+ |

**Strengths:**
- **Industry standard** — most widely adopted Go CLI library
- **Huge ecosystem** — cobra-cli generators, `cobra-gen`, etc.
- **pflag compatibility** — can wrap existing flag parsing if needed during migration
- **Extensive customization** — hooks, annotations, persistent/pre-run flags

**Weaknesses:**
- **Heavier dependency tree** (~500 KB compiled)
- **More boilerplate** than urfave/cli for simple commands (requires `RunE` functions, `PersistentFlags`, etc.)
- **Steeper learning curve** — more concepts to understand (root command, subcommands, persistent flags, local flags, hooks)

**Migration effort estimate:** ~2–3 weeks; the extra complexity makes it less ideal for our current codebase size.

### 2.3 Other Notable Alternatives

| Library | Notes |
|---------|-------|
| `github.com/alecthomas/kong` | Declarative struct-based CLI parser; excellent validation; used by Docker Compose v2, Terratest |
| `github.com/urfave/cli/v3` (already listed) | Best balance of simplicity + features for our use case |
| `github.com/spf13/cobra` | Overkill for our scale; better for large monorepos with many CLI tools |
| `github.com/alecthomas/kingpin` | Legacy; unmaintained since 2020 — **not recommended** |
| `github.com/jessevdk/go-flags` | Simpler, but less actively maintained than urfave/cli |

---

## 3. Recommendation

### Primary: **urfave/cli/v3**

**Rationale:**
1. Our CLI codebase is ~5,700 lines total across three components — urfave/cli's lightweight footprint and automatic help generation map well to this scale.
2. The current manual parsing pattern (linear scan + `strconv` calls) maps 1:1 to urfave/cli's typed flag declarations.
3. Help text inconsistency (P1) is solved by the library's built-in help renderer.
4. Validation errors (P2) are handled automatically with typed flags.
5. Shell completion (P7) is a one-line addition per binary.

### Migration Strategy

| Phase | Scope | Deliverable |
|-------|-------|-------------|
| **Phase 0** | Assessment (this doc) | ✅ Done |
| **Phase 1** | `norman` client — rewrite `main.go` + all handler files to use urfave/cli v3 commands. Keep existing API behavior identical. | New CLI with automatic help, typed flags, consistent errors |
| **Phase 2** | `roger` — single-file replacement with one `cli.Command` tree | Simplified from ~325 lines of manual parsing to ~80 lines of declarative config |
| **Phase 3** | `norman-server` — replace the minimal `-v`/`-p` flag loop with urfave/cli's `Flag` declarations | Consistent with client; no behavioral change |
| **Phase 4** | Add shell completion scripts (Bash, Zsh, Fish) via `cli.Command.Completion` | One-time per binary |
| **Phase 5** | Update `docs/` help text references; remove `pkg/config/help.go` (help is now generated by the library) | Clean up dead code |

### Estimated Impact

| Metric | Before | After (estimated) |
|--------|--------|-------------------|
| CLI code lines | ~5,700 | ~2,800 (~50% reduction) |
| Help text strings | ~15 scattered `fmt.Println`/string literals | 0 (auto-generated) |
| Manual `strconv` calls | ~30+ across handlers | 0 (typed flags handle this) |
| Flag validation errors at runtime | Ad-hoc messages | Standardized library messages |

---

## 4. Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Breaking existing scripts that depend on current flag order/semantics | Keep `--help` output backward-compatible; run integration tests against all subcommands |
| Migration is large (5,700 lines) | Phase-by-phase per-component; each phase can be reviewed independently |
| urfave/cli v3 has a learning curve | Start with the simplest command (`norman providers list`) as a proof-of-concept before full migration |
| `norman-server` currently uses Echo + minimal flags — no major change needed there | Phase 3 is low-risk; mostly replace the `for arg := range os.Args[1:]` loop |

---

## 5. Conclusion

The current manual flag parsing across `norman`, `norman-server`, and `roger` introduces **repetition, inconsistency, and maintenance overhead** that a mature CLI library would solve in one pass. **urfave/cli/v3** is the best fit for our codebase size (small-to-medium) and provides automatic help generation, typed flag validation, shell completion, and a clean command tree structure — all of which directly address the 7 problems identified in §1.3.

The migration can be done incrementally without breaking existing behavior, and the expected outcome is ~50% reduction in CLI-related code with zero loss of functionality.

---

*This document is assessment-only. No code changes have been made.*
