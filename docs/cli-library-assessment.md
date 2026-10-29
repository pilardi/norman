# CLI Library Assessment — Replacing Manual Flag Parsing

> Date: 2026-10-29
> Branch: `feat/cli-library-assessment`
> Status: **norman client migrated; roger and norman-server pending**

---

## 1. Current State (Post-Migration)

### 1.1 Components and Their CLI Code

| Component | Location | Lines of CLI code | Entry point | Status |
|-----------|----------|-------------------|-------------|--------|
| `norman` (client) | `cmd/norman/` | ~2,428 → ~1,570 lines across 8 files | `main.go` + subcommand handlers | ✅ **Migrated** |
| `norman-server` | `cmd/norman-server/` | ~3,022 lines across 10 files | `main.go` (minimal flags) | ⏳ Pending |
| `roger` | `cmd/roger/main.go` | ~325 lines | Single-file CLI | ⏳ Pending |

### 1.2 Migration Summary — norman client

**Library chosen:** `urfave/cli/v3` (v3.2.x)

**Changes applied:**
- Replaced all manual flag parsing loops with typed urfave flags (`BoolFlag`, `StringFlag`, `Float64Flag`, `IntFlag`)
- Added `context.Context` parameter to all Action functions
- Replaced scattered `fmt.Println("...")` help text with consolidated `sessionHelpText`/`skillsHelpText` constants
- Fixed Context→Command API migration (v2 `*cli.Context` → v3 `*cli.Command`)
- Added package-level `verbose` and `selectedProvider` vars for cross-file access
- Added `serverURL()` helper in http.go, restored `logRequest()`, `listEmergentTasks()`
- Added `shortID()` helper in sessions.go
- All lint checks pass (golangci-lint v2): **0 issues**

**Code reduction:** ~858 lines removed across 8 files (~35% reduction)

### 1.3 Patterns Previously Used (Before Migration)

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

### 1.4 Identified Problems (Pre-Migration)

| # | Problem | Example | Impact |
|---|---------|---------|--------|
| 1 | Repetitive flag parsing boilerplate | `for i := 0; i < len(args); i++ { if args[i] == "--flag" ... }` repeated ~40+ times across files | High code duplication, error-prone |
| 2 | Inconsistent help text | Each handler file has its own `fmt.Println("...")` block with varying formatting | Poor UX, hard to maintain |
| 3 | No shell completion | Manual flags → no tab-completion for commands/flags | Developer friction |
| 4 | Error-prone type conversion | `strconv.ParseFloat`, `strconv.Atoi` with manual error handling scattered throughout | Runtime panics if parsing fails |
| 5 | No validation | Flags accept any string without type checking or range validation | Silent data corruption |
| 6 | Global state coupling | `verbose`, `selectedProvider` globals accessed across files without clear ownership | Hard to test in isolation |
| 7 | Help text drift | Inline help strings diverge from actual flag names over time | Outdated documentation |

---

## 2. Library Comparison

| Feature | urfave/cli/v3 | cobra | kong |
|---------|---------------|-------|------|
| API style | Declarative struct tags | Builder pattern (`cmd.Flags().String()`) | Struct-based with constraints |
| Shell completion | Built-in (Bash, Zsh, Fish) | Yes (via `cobra/shellcompletion`) | No built-in |
| Help generation | Auto-generated from flags | Auto-generated | Manual help text |
| Context support | `context.Context` in Action | Via `persistentPreRunE` | Via middleware |
| Size (compiled binary) | ~1.2 MB | ~2.1 MB | ~0.8 MB |
| Ecosystem adoption | High (Docker, Helm, etc.) | Very high (Kubernetes ecosystem) | Moderate |
| Learning curve | Low (simple struct API) | Medium (builder pattern) | Medium (constraint system) |

**Decision:** `urfave/cli/v3` chosen for its simpler API surface and built-in shell completion, which aligns with the codebase's philosophy of "less is more."

---

## 3. Migration Plan (Phased)

### Phase 1: norman client ✅ **COMPLETED**
- [x] `cmd/norman/main.go` — Replace manual dispatch with urfave Command tree
- [x] `cmd/norman/config.go` — Replace `--set`/`--json` parsing with typed flags
- [x] `cmd/norman/providers.go` — Replace flag loops with `cmd.String()`, `cmd.Float64()`
- [x] `cmd/norman/workspaces.go` — Replace manual arg parsing with `cmd.Args().First()/Slice()`
- [x] `cmd/norman/sessions.go` — Replace subcommand dispatch with urfave Commands tree
- [x] `cmd/norman/skills.go` — Replace subcommand parsing with urfave Commands tree
- [x] `cmd/norman/chat.go` — Consolidate help text, fix REPL loop
- [x] `cmd/norman/http.go` — Fix `logRequest()` and `serverURL()` references
- [x] Run `golangci-lint`: **0 issues**

### Phase 2: roger (`cmd/roger/main.go`) ⏳ PENDING
- Replace manual flag parsing for `notify`, `ping`, `status`, `log` subcommands
- ~325 lines → estimated ~150 lines after migration

### Phase 3: norman-server (`cmd/norman-server/`) ⏳ PENDING
- Migrate `-v`/`-p` flag loop to urfave flags
- ~3,022 lines across 10 files — largest migration target
- Note: Uses Echo HTTP framework (not migrated) alongside CLI flags

### Phase 4: Shell Completion ⏳ PENDING
- Generate Bash, Zsh, Fish completion scripts via `cli.Command.Completion()`
- Add `make completion` target to Makefile

### Phase 5: Cleanup ⏳ PENDING
- Remove `pkg/config/help.go` (help auto-generated by library)
- Remove `printprovidersHelp()`, `printSessionHelp()`, `printChatHelp()` functions
- Update README with new CLI usage examples

---

## 4. Estimated Impact

| Metric | Before | After (norman client) | Reduction |
|--------|--------|----------------------|-----------|
| Lines of CLI code | ~2,428 | ~1,570 | **~35%** |
| Manual flag parsing loops | ~40+ | 0 | **100%** |
| Help text strings | ~15 scattered | 2 consolidated constants | **87%** |
| Type conversion calls | ~30+ `strconv.*` | 0 (typed flags) | **100%** |

---

## 5. Next Steps

1. **Migrate roger** — single file, straightforward replacement
2. **Migrate norman-server** — larger scope, may need incremental approach
3. **Add shell completion** — `make completion` target
4. **Update documentation** — README, CLI examples
5. **Run full test suite** — verify no behavioral regressions

---

*Assessment created during session; migration of norman client completed on branch `feat/cli-library-assessment`.*
