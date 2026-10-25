# Memory Architecture Enhancement Proposal

> **API Spec**: [Full Reference](api-reference.html)


> **Status:** Draft — awaiting review before implementation.
> **Source of ideas:** gajae-way (https://github.com/Yeachan-Heo/gajae-way) memory system and session bootstrap mechanism.
> **Date:** 2026-10-15

---

## Overview

This proposal introduces two enhancements to norman's Long-Term Memory (LTM) system:

1. **Default axis structure** — a built-in, zero-config memory layout that works out of the box with sensible defaults (flat wiki + frontmatter types).
2. **Session bootstrap** — a bounded context-injection mechanism for new session epochs, replacing the current full workspace scan with a curated pointer set.

Customization via `axes.json` is a *later* capability; the default structure works without any user-facing config file.

---

## Part 1: Default Axis Structure (No Config File Needed)

### Current State

norman's memory uses flat markdown pages classified by `type:` frontmatter (`entity`, `source`, `trend`, `task`). This is simple but lacks structural rules per category and has no auto-validation.

### Proposal: Built-in Defaults

The system ships with a **default axis structure** encoded in the binary — no `axes.json` required at startup. The defaults are:

| Default Axis | Layout | Nesting | Append-Only | Retrieval Priority |
|--------------|--------|---------|-------------|-------------------|
| `entity` | free | flat | false | 80 |
| `task` | dated | nested | false | 60 |
| `trend` | dated | nested | true | 40 |
| `source` | free | flat | false | 50 |

These map to the existing flat wiki layout: entries live at `.norman/memory/<type>/<title>.md`. No subdirectories beyond the type folder. The system creates these directories lazily (only when an entry of that type is first written).

### Behavior Without `axes.json`

- **Index generation**: `index.md` is auto-generated from frontmatter `type:` fields — no registry file needed.
- **Audit**: structural checks run against the default rules (layout violations, duplicates, orphan files outside known types).
- **Retrieval**: BM25 ranking + priority weighting by type.
- **No files created until needed**: if a workspace has only `entity` and `task` entries, no `trend/` or `source/` directories are written to disk.

### Benefits of Defaults-First

- Zero-config: works immediately for any workspace.
- No per-workspace boilerplate — the user doesn't need to maintain an `axes.json` file.
- The default structure is stable and well-tested; customization is opt-in later.

---

## Part 2: Session Bootstrap (No Config File Needed)

### Problem

norman's current cold-start process scans the entire workspace file tree + key files, which can be expensive for large repos and wastes tokens on irrelevant context.

### Solution: Bounded Context Injection

Before the first turn in a new session epoch, inject a **Session bootstrap** section (≤ 8 KiB UTF-8 bytes) containing curated pointers to relevant memory entries:

```
[SESSION BOOTSTRAP]
identity: workspace="norman", origin="cli/DM/12345"
applied_at: 2026-10-15T17:00:00Z
included_sections: [identity, today_entries, yesterday_entries, channel_record, navigation_map]
truncation: none
diagnostics: []

## Today's Entries (excerpt)
- `daily/2026-10-15.md` — 3 entries captured
- `tasks/TASK-42.md` — status updated to "in_progress"

## Yesterday's Entries (excerpt)
- `daily/2026-10-14.md` — 7 entries, task TASK-41 completed

## Navigation Map
- See `index.md` for full catalog by type and tags.
- Recent: `tasks/TASK-42.md`, `entities/api-gateway.md`

[END BOOTSTRAP]
```

### Allocation Strategy

Memory budget is allocated by recoverability priority:
1. **Mandatory**: identity + applied time (always included)
2. **High share**: today's entries, yesterday's entries
3. **Remainder**: channel record, rules index, navigation map pointers
4. **Optional**: daily source excerpts (may be truncated at 4 MiB ceiling per file)

### Lifecycle Integration

- Bootstrap is durable in the session row (`last_bootstrapped_epoch`).
- `/new` establishes a new epoch; next turn receives fresh bootstrap.
- Failure before terminal success retries with same stable ID; intermediate delivered replies are consumed to prevent harmful replays.
- On crash, pending bootstrap safely re-presented under same idempotent ID.

---

## Part 3: Per-Workspace `axes.json` (Opt-In, Later Phase)

### When It's Needed

Once the default structure is stable and working, users who want to customize beyond defaults can add a workspace-local `memory/axes.json`:

```json
{
  "version": 1,
  "axes": [
    {
      "id": "entity",
      "root": "entities",
      "retrievalPriority": 90,
      "promotesTo": ["task"]
    }
  ]
}
```

### Rules for `axes.json`

- **Workspace-local only** — no system-level or installation-wide config. Each workspace manages its own overrides.
- **Partial override**: stated fields win; omitted fields keep the default value. No need to redeclare everything.
- **Opt-in**: if `axes.json` doesn't exist, the system falls back to built-in defaults (Part 1).
- **Fail-closed on errors**: malformed registry or promotion cycles prevent startup rather than silently dropping axes.

### Customization Capabilities

| Capability | Without `axes.json` | With `axes.json` |
|------------|---------------------|------------------|
| Layout per type | Fixed (free/nested as default) | Overrideable |
| Retrieval priority | Fixed (80/60/40/50) | Custom scores |
| Promotion paths | None | Configurable acyclic graph |
| Orphan policy | `any-depth` | `partitioned` or `any-depth` |
| Append-only | Per default axis | Overrideable per axis |

---

## Implementation Plan

### Phase 1: Default Structure + Bootstrap (Weeks 1–2)

- [ ] Implement default axis rules in Go (no config file needed)
- [ ] Lazy directory creation — only write `entities/`, `tasks/` etc. when first entry of that type is created
- [ ] Auto-generate `index.md` from frontmatter types
- [ ] Structural audit: `map_dangling`, `duplicate_file_hash`, `orphan_file` checks
- [ ] Session bootstrap injection (~8 KiB cap) at session start
- [ ] Wire bootstrap into session lifecycle (`/new`, epoch tracking)

### Phase 2: Per-Workspace `axes.json` Override (Week 3+)

- [ ] Schema definition and parser for workspace-local `memory/axes.json`
- [ ] Merge logic: declared fields override defaults, omitted keep default values
- [ ] Promotion cycle detection at startup
- [ ] Extend audit to validate declared axes against actual file layout
- [ ] Update retrieval scoring to use declared priorities

### Phase 3: Integration & Testing (Week 4)

- [ ] Integrate audit into existing `memory/` engine tests
- [ ] Add e2e test for default structure with no `axes.json`
- [ ] Benchmark cold-start time improvement vs. full workspace scan
- [ ] Update `AGENTS.md` schema with new maintenance rules

---

## Migration Notes

- Existing flat markdown pages (current norman wiki) remain valid — they map to the default `entity` axis with `layout: free`, `nesting: flat`.
- No files are created until an entry of that type is first written.
- Git history of existing entries is preserved byte-identically during transition.

---

## Success Metrics

| Metric | Current | Target after enhancement |
|--------|---------|--------------------------|
| Cold-start context size | Full workspace scan (~50–200 KB) | Bounded bootstrap (~8 KB) |
| Memory validation | Manual (human reads files) | Automated audit with exit codes |
| Retrieval accuracy | BM25 on frontmatter only | BM25 + axis priority weighting |
| Orphan detection | None | `map_dangling`, `orphan_file` diagnostics |
